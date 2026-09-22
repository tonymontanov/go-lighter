# handoff.md — go-lighter SDK

Context document for continuing work across sessions. **Update after every
significant task** (architecture change, new module, refactoring).

Last update: 2026-09-23 — v1.0 SDK code complete: `go test ./... -race`
green, staticcheck clean, cgo-free + linux/amd64 builds OK, keyless mainnet
smoke (`make smoke`) passed. Nothing is committed yet (all files untracked in
git; awaiting the owner's go-ahead). Next: the desk connector (M6).

---

## 1. Role & stack

- **What:** low-latency Go SDK for **Lighter** (zkLighter: perp DEX on its own
  zk-rollup, on-chain order book, sequencer-side matching) for an HFT /
  market-making desk. Sibling of `go-okx`, `go-bybit`, `go-kucoin`, `go-aster`,
  `go-hyperliquid`; consumed by `sleipnir-trading-core`.
- **Module:** `github.com/tonymontanov/go-lighter` (v1.x); `go 1.24` in go.mod
  (the desk builds with Go 1.24.6, `CGO_ENABLED=0`; CI enforces a cgo-free build).
- **Dependencies (4 + transitive):** `gorilla/websocket`, `json-iterator/go`,
  `shopspring/decimal` (the sibling family) + `elliottech/poseidon_crypto`
  v0.0.15 (Goldilocks field, Poseidon2, ECgFp5 Schnorr — the primitives the
  official `lighter-go` uses; pulls `consensys/gnark-crypto`). **Not**
  `lighter-go` itself (go-ethereum + blst / kzg / verkle; shared-library
  singleton design). Approved by the owner on 2026-09-22.
- **Source ToR:** `docs/TS-SINGLE-EXCHANGE-SDK.md` / `-RU.md`.
- **References:** official docs https://apidocs.lighter.xyz (markdown: append
  `.md`; index `llms.txt`); OpenAPI = `openapi.json` in `elliottech/lighter-python`
  (v1.1.4); signing reference `elliottech/lighter-go` **v1.0.10** (commit
  9d38261) — used only by `scripts/gen-vectors`.

## 2. Architecture

### 2.1 Two-layer principle (no parallel copy-paste)

One **common layer** implements every request once; each **section** is a thin
specialisation that contributes only a `domain.Profile` (section name,
market_type filter `perp` / `spot`, market registry). Sections never import
each other. Section names mirror the exchange's vocabulary.

```
perpetuals.Trading().CreateOrder(ctx, req, opts)
   └─ domain.CreateOrder(ctx, engine, profile, req, opts, transport)          ← unified
        └─ engine.Send(ctx, tx, opts)  nonce lane → fill header → validate → Poseidon2 hash
                                        → Schnorr sign → tx_info JSON → form → REST sendTx
                                        (or WS jsonapi/sendtx) → TxReceipt → lane outcome
```

| Section (official name)                  | Package      | Core constant                     | Stage |
|------------------------------------------|--------------|-----------------------------------|-------|
| Perpetuals (`market_type: perp`)         | `perpetuals` | `lighter_perpetuals[_testnet]`    | v1.0 🔧 |
| Spot (`market_type: spot`)               | `spot`       | `lighter_spot[_testnet]`          | v2.5 📋 |
| Account (API keys, tokens, tier, subs)   | `account`    | —                                 | v2.5 📋 |
| Bridge (deposits, withdrawals, transfers)| `bridge`     | —                                 | v2.5 📋 |
| Public pools & staking                   | `pools`      | —                                 | v2.5 📋 |

Naming (`perpetuals`) and the v2.5 list approved by the owner on 2026-09-22.

### 2.2 Folder structure (as written)

```
go-lighter/
├── client.go config.go errors.go logger.go metrics.go rate-limit-event.go
│   tx-tracker.go doc.go            root package `lighter` = public face of the common layer
├── types/                          layer-1 public types
│   ├── fixed.go precision.go       Fixed (int64, 8 decimals) + static per-market grid
│   ├── enums.go                    order type / tif / cancel-all tif / margin mode / tx type /
│   │                               order status / trigger status / tx status / resolutions
│   ├── orders.go                   CreateOrder / Modify / Cancel / CancelAll requests, SendOptions, Order view
│   ├── market.go account.go tx.go  markets, book, ticker, trade, candle, stats; account, position,
│   │                               balance, api key, limits; TxReceipt / BatchReceipt / Transaction / TxOutcome
├── internal/
│   ├── codec/        jsoniter chokepoint (CaseSensitive: candles carry "v" and "V")
│   ├── lterr/ ltlog/ ltmet/   error model (Kind + exchange Code), logger, metrics
│   ├── signing/      key manager (40-byte LE scalar), allocation-free Poseidon2 sponge on
│   │                 p2.Permute, Schnorr (library), auth token (gnark-flavour Poseidon2, as lighter-go)
│   ├── tx/           transactions: CreateOrder(14) CancelOrder(15) CancelAllOrders(16)
│   │                 ModifyOrder(17) UpdateLeverage(20) CreateGroupedOrders(28) UpdateMargin(29),
│   │                 L2TxAttributes (8 attribute types), paired Hash + AppendInfo writers,
│   │                 vectors_generated_test.go (19 vectors from lighter-go, byte-identical)
│   ├── nonce/        lanes per (account, api key): sequential (+1, serialised sends, rollback /
│   │                 resync) and skip (monotonic ms + SkipNonce attribute, lock-free)
│   ├── ratelimit/    weights table, tiers, sendTx bucket by staked LIT, tx-type buckets,
│   │                 lock-free 60 s windows, 429/405 cooldown, observer events
│   ├── rest/         GET + form POST under /api/v1, envelope {code,message} mapping,
│   │                 zero-alloc query / form / JSON-array encoders
│   ├── ws/           supervised /stream conn: subscribe/unsubscribe (+auth per subscription),
│   │                 ping/pong both ways, resubscribe + Reset, jsonapi posts by id
│   ├── markets/      atomic market registry (symbol ↔ id ↔ precision), background refresh
│   ├── orderbook/    snapshot + delta book with begin_nonce continuity (zero-alloc updates)
│   ├── engine/       THE unified request layer: Query / PostJSON / Send / SendBatch, auth-token
│   │                 cache, rate accounting, lane outcome mapping
│   └── domain/       unified functions parameterised by Profile: markets, trading, account, streams
├── perpetuals/       section: client.go trading.go account.go market.go stream.go + contract_test.go
├── scripts/gen-vectors/   SEPARATE module (depends on lighter-go v1.0.10): `make vectors`
├── scripts/run.sh    runs an example with .env exported
├── examples/         market-data (keyless smoke), simple-trade (gated, REAL funds)
└── docs/             ToR (RU/EN), API-NOTES.md (verified facts + discrepancies), DEPLOY.md
```

Dependency direction: `types` and `internal/*` never import the root; the root
imports `internal/*`; sections import the root + `internal/*`. The root never
imports a section (typed `perpetuals.NewClient(client)`, no `any`).

### 2.3 Key protocol facts (verified 2026-09-22; live probes on mainnet, keyless)

- REST `https://mainnet.zklighter.elliot.ai/api/v1/…` (chain id 304),
  `https://testnet.zklighter.elliot.ai/api/v1/…` (chain id 300); WS `wss://…/stream`.
  235 perp markets on mainnet, ids up to 4095; `size_decimals == supported_size_decimals`
  on every market. No rate-limit headers in responses (CloudFront).
- Writes = signed L2 transactions: `POST sendTx` (form: `tx_type`, `tx_info`,
  `price_protection`) / `POST sendTxBatch` (form: `tx_types` = JSON int array,
  `tx_infos` = JSON array of tx_info STRINGS). Reply `{code, message, tx_hash,
  predicted_execution_time_ms, volume_quota_remaining}`. `code=200` = accepted by
  the API server only; execution confirmed by the sequencer → `account_tx`
  (tx status 0 Failed / 1 Pending / 2 Executed, `event_info.ae` app error).
- Signing: hash = Poseidon2 (plonky2 flavour) over `[chainId, txType, nonce,
  expiredAt, accountIndex, apiKeyIndex, …fields]`, aggregated with the attributes
  hash when attributes are present; signature = Schnorr over ECgFp5, 80 bytes
  (s ‖ e, LE) base64 in `Sig`. Hash deterministic; signature randomised (vectors
  use lighter-go's fixed-k `SchnorrSignHashedMessage2`). `tx_info` = encoding/json
  of the lighter-go struct, `"L2TxAttributes":null|{"4":1,...}` last.
- Measured (Apple M4 Pro): Poseidon2 hash 3.3 µs / 0 allocs; tx_info JSON 120 ns /
  0 allocs; sendTx form 450 ns / 0 allocs; nonce acquire 4 ns; Schnorr **202 µs /
  69 allocs** (accepted for v1.0; allocation-free scalar mult is a later item).
  The whole local send path (fill + hash + sign + JSON + form) = 206 µs / 69 allocs.
- Private key = 40-byte LE scalar (hex, optional 0x); public key = 40-byte point,
  the apikeys endpoint prints it WITHOUT 0x. API key indices 0–254, 255 = all in
  queries (docs disagree whether 0–1 or 0–3 are reserved).
- Nonce per API key: strictly `old+1`; `SkipNonce` attribute allows monotonic
  values below 2^47-1; API-level error (code ≠ 200) does NOT consume the nonce.
  The official Python SDK holds the key's lock through the send (we do the same
  in sequential mode). Batch: one account + one key, increasing nonces; REST
  error text says max 50, WS docs say 15.
- Auth token `{expiry}:{account}:{apiKey}:{schnorr sig hex}` (≤ 8 h), hashed
  with the gnark flavour of Poseidon2 (`poseidon2_goldilocks`, as lighter-go);
  header `Authorization` (also accepted as `auth` query param — live error text).
  Missing token on a private endpoint → code 20001.
- WS (live): greeting `{"session_id":…,"type":"connected"}`; pushes
  `{"type":"subscribed/<c>"|"update/<c>","channel":"<c>:<args>",…}`; error frames
  `{"error":{"code":30005,"message":"Invalid Channel"}}` (no "type"); jsonapi
  replies carry NO "type", only the id: `{"error":{…},"id":"probe-1"}` (success
  shape not yet observed — decoder accepts top-level or `data` envelope);
  order_book snapshot has `nonce` and `begin_nonce:0`, every update's
  `begin_nonce == previous nonce` (verified over 4 updates); `account_all_positions`
  also carries `bo_positions`; `recentTrades` carries `market_kind:"perps"`.
- Rate limits: Standard 60 req/min (+ 24 000 weighted cap); Premium/Plus 24 000
  weighted; Builder 240 000; sendTx bucket by staked LIT (4 000…48 000/min);
  documented tx-type buckets only (withdraw 2, leverage 40, …) — the page's
  "Default 40/min" is NOT enforced (config `DefaultTxTypeLimit`); cooldown 405 →
  60 s, 429 → weight/(limit/60).
- Colocation: AWS Tokyo `ap-northeast-1a` (apne1-az4).

### 2.4 Deliberate deviations from the ToR / sibling SDKs

| Topic | Decision | Why |
|---|---|---|
| Crypto | `poseidon_crypto` only; thin lighter-go layer ported; vectors generated from lighter-go in a separate module | no go-ethereum in the graph; hot path allocation-free except the library's Schnorr |
| Numerics | `types.Fixed` in requests / streams, `decimal` in money fields | zero-alloc; static per-market grid (`Precision.WirePrice / WireSize`) |
| Two confirmation levels | `TxReceipt` (API) vs `TxOutcome` (sequencer, `account_tx`) + `lighter.TxTracker` | the exchange separates them |
| Nonces | per-key lanes; sequential mode serialises the round trip per key; skip mode lock-free | ingestion-order nonce check |
| Order book | snapshot + delta with `begin_nonce` continuity; gap → automatic resubscribe | Lighter has a true delta feed |
| WS trading | `Trading().WS()` implemented but marked experimental | reply shape only partially observed |

### 2.5 Discrepancies found (to move into docs/API-NOTES.md)

Batch size 50 (error 21514) vs 15 (WS docs); reserved key indices 0–1 vs 0–3;
tx types 33–37 / 41–45 exist in lighter-go but not on the constants page; auth
token's last part is a signature, not "random_hex"; live orderBookDetails has
undocumented keys (`is_frozen`, `settlement_*`, `operator_account_index`,
`outcome`, `start/end_timestamp`); `account_all` example shows objects where the
structure says arrays; jsonapi reply shape undocumented; testnet `/api/v1/status`
answers 404; `account` returns `{"total","accounts":[…]}` (DetailedAccounts) with
both `index` and `account_index`, plus undocumented `bo_positions`, `agent_enabled`.

## 3. Roadmap

### ✅ Done (2026-09-22) — v1.0 SDK code
- M0 tooling: `Makefile`, `.golangci.yml`, GitHub Actions (fmt, vet, race,
  cgo-free build, lint), `.gitignore`, `.env.example`, `scripts/run.sh`.
- M1 signing + transactions: 19 byte-identical vectors vs lighter-go v1.0.10
  (hash, signature with fixed k, tx_info JSON), validation matrix ported.
- M2 transport: REST, supervised WS (live shapes verified), rate limiter.
- M3 common layer: engine, nonce lanes, registry, order book, domain functions
  (markets, trading, account, streams), TxTracker.
- M4 `perpetuals` section written + contract tests (mock REST + WS).

### ✅ Done (2026-09-23)
- `TestStreams` fixed (mock replayed the gap on every resubscribe; test
  handlers blocked the read loop) + a real data race fixed in
  `domain.WatchOrderBook` (the resubscribe closure was assigned after
  registration; now the subscription is fully built before `Subscribe`).
- Examples `market-data` (keyless) and `simple-trade` (gated); README,
  CHANGELOG, docs/API-NOTES.md (18 discrepancies), docs/DEPLOY.md.
- Keyless live smoke on MAINNET: 235 perp markets, REST details / book /
  trades / candles, 8 s of ticker + maintained book + stats + trades: 43
  tickers, 88 book updates, no gaps. staticcheck clean (ST1005 fixed).

### 🔧 In progress — M6 desk connector
- `git add` + first commit of the SDK (owner to confirm), later tag `v1.0.0`
  after the owner's live order-path check (`examples/simple-trade`, REAL funds).
- Branch `lighter-connector` from `qa` in sleipnir-trading-core, mirroring the
  core's `hyperliquid-connector` commits: constants → `connectors/lighter/
  {common,perpetuals}` → rate-limiter strategy → wiring → docs. While the SDK
  is unpublished the core's go.mod needs a local `replace` (forbidden on merge
  by `make check-gomod-replace` — drop it once `v1.0.0` is tagged).

### 📋 v2.0 — full perps trading
TP/SL (types 2–5 are already accepted by `CreateOrder`), grouped orders
(OCO/OTO/OTOCO, `tx.CreateGroupedOrders` written, no domain function yet),
TWAP (type 6), `order_version` (done in ModifyOrder), sub-accounts, key pool
(`Config.ExtraKeys` + `SendOptions.APIKeyIndex` already wired; add rotation),
leverage / margin (done), WS `jsonapi/sendtx` (written, experimental),
maker-only API keys, self-trade attributes (in `tx.Attributes`).

### 📋 v2.5 — everything else
`bridge`, `pools`, `account` (changePubKey with L1 signature, tokens, tier),
history endpoints, `spot` section.

## 4. Rules & code style

- English comments and docs everywhere (public project). File header block
  `FILE / DESCRIPTION / MAIN FUNCTIONS / DEPENDENCIES`; a comment on every
  exported identifier and on every const of an enum.
- camelCase locals, PascalCase exports, initialisms upper-case (`MarketID`,
  `URL`), millisecond timestamps suffixed `Ms`, microseconds `Us`. Kebab-case
  file names, tests `*_test.go`.
- **Explicit declarations:** `var x T = ...`; `:=` only in `if err := ...`
  guards, `select` cases and tests.
- `context.Context` first; JSON only through `internal/codec`; no panics in
  library code (only `MustParseFixed`).
- **Hot path = zero allocations** (tx build, hash, JSON body, form, nonce, rate
  window, price scaling, book updates). The Schnorr signature's 69 allocations
  are the library's and are tracked. A hot-path change ships with before/after
  benchmarks (`make bench`); allocs/op must not grow.
- Validation before network; the SDK never rounds caller prices silently.
- **Never invent API fields.** Model documented keys, decode leniently; record
  docs-vs-live discrepancies in docs/API-NOTES.md (to be created).
- Sections never import each other; shared code moves down into `internal/domain`.
- Tests: stdlib `testing`, inline fixtures, no network. Live checks only via
  examples behind `LIGHTER_ALLOW_LIVE=1`.

## 5. Integration secrets (names only — NEVER real keys)

| Variable | Meaning |
|---|---|
| `LIGHTER_PERPETUALS_API_KEY` | account index |
| `LIGHTER_PERPETUALS_SECRET_KEY` | API key private key(s), 40-byte hex, comma-separated |
| `LIGHTER_PERPETUALS_PASSPHRASE` | API key index(es), comma-separated, same order |
| `LIGHTER_ALLOW_LIVE=1` | safety gate of the order-sending example (MAINNET!) |
| `LIGHTER_TESTNET=1` | switch the examples to testnet (no desk credentials there) |

Endpoints: mainnet `https://mainnet.zklighter.elliot.ai` / `wss://mainnet.zklighter.elliot.ai/stream`;
testnet `https://testnet.zklighter.elliot.ai` / `wss://testnet.zklighter.elliot.ai/stream`.
`.env` is git-ignored; keys are never logged, wiped from `Config` after
`NewClient` and zeroed on `Close()`.
