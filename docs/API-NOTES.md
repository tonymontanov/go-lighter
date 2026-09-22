# Lighter API — verified notes

Working notes behind the SDK. Every statement is tagged with its source:

- **[DOC]** official docs https://apidocs.lighter.xyz (markdown: append `.md`; index `llms.txt`), fetched 2026-09-22;
- **[OAS]** `openapi.json` of `elliottech/lighter-python` v1.1.4;
- **[GO]** official signer `elliottech/lighter-go` v1.0.10 (commit `9d38261`);
- **[PY]** official `elliottech/lighter-python` v1.1.4 (usage patterns);
- **[LIVE]** read-only public requests to mainnet on 2026-09-22 (no keys).

Rule of the project: nothing here is guessed. "Not documented" means exactly that.

## 1. Where the sources disagree or fall short

| # | Topic | Finding | SDK decision |
|---|---|---|---|
| 1 | Batch size | [DOC] WebSocket page: "up to 15 transactions"; [DOC] error 21514: "maximum 50 transactions allowed per batch". | REST batches ≤ 50, WS batches ≤ 15 (`engine.MaxBatchSizeREST/WS`). |
| 2 | Reserved API key indices | [DOC] get-started: 0–1 reserved; [DOC] api-keys: 0–3 reserved; [GO] validates 0..254. | Config accepts 2..254 by convention; 255 is rejected. |
| 3 | Transaction types | [DOC] constants page lists up to type 30; [GO] also has 33–37 (staking), 41–45 (account config, integrator). | `types.TxType` lists the [GO] set. |
| 4 | Auth token | [DOC] `{expiry}:{account}:{apiKey}:{random_hex}`; [GO] the last part is the Schnorr signature of the message. | Implemented as [GO]. |
| 5 | Auth token hashing | [GO] hashes the token message with the gnark flavour of Poseidon2 (`poseidon2_goldilocks`), transactions with the plonky2 flavour. | Both used exactly as [GO]; parity tests cover transactions. |
| 6 | Signature randomness | [GO] `SchnorrSignHashedMessage` samples k from crypto/rand; byte-for-byte vectors are impossible without a fixed k. | Vectors use `SchnorrSignHashedMessage2` (fixed k) and verify with the reference verifier. |
| 7 | jsonapi reply shape | [DOC]: "id is returned in the response", nothing else. [LIVE] error replies: `{"error":{"code":21109,"message":"api key not found"},"id":"probe-1"}` — no `type`. Success shape not observed. | Decoder accepts top-level or `data`-wrapped envelopes; WS trading marked experimental. |
| 8 | WS error frames | [DOC] nothing. [LIVE] `{"error":{"code":30005,"message":"Invalid Channel"}}` (no `type`, no `channel`). | Routed to `OnServerError`; posts matched by id first. |
| 9 | orderBookDetails keys | [LIVE] carries `is_frozen`, `settlement_*`, `operator_account_index`, `outcome`, `start_timestamp`, `end_timestamp`, `default_price` — absent from [OAS]. | Only documented keys modelled; decoding lenient. |
| 10 | `account` endpoint | [OAS] DetailedAccounts; [LIVE] `{"total":1,"accounts":[…]}` with both `index` and `account_index`, plus undocumented `bo_positions`, `agent_enabled`. | `types.Account` uses `index`; extra keys ignored. |
| 11 | `account_all` example | [DOC] shows `shares` / `trades` / `funding_histories` as objects while the structure says arrays / maps. | Channel not exposed in v1.0 (positions / orders / trades have dedicated channels). |
| 12 | Order book continuity | [DOC] "check that begin_nonce matches the nonce of the previous update". [LIVE] snapshot has `nonce` and `begin_nonce: 0`; every update's `begin_nonce` equals the previous frame's `nonce` (4 consecutive updates checked). | `internal/orderbook` gap = `begin_nonce != last nonce` → resubscribe. |
| 13 | Private endpoints without token | [LIVE] code 20001 "invalid param : auth query param and Authorization header are empty" (the token is also accepted as an `auth` query parameter). | Header `Authorization: <token>`. |
| 14 | "Default 40 requests / minute" per tx type | [DOC] rate-limits page lists it next to the per-type limits; applying it to order transactions contradicts the sendTx buckets (4 000–48 000 / min). | Not enforced; `Config.RateLimit.DefaultTxTypeLimit` opt-in. |
| 15 | Which layer answers 429 vs 405 | [DOC] cooldowns: firewall 60 s static, API servers `weight/(total/60)`; the status codes are not attributed. | 405 → 60 s, 429 → weighted formula. |
| 16 | Testnet status endpoint | [LIVE] `GET /api/v1/status` on testnet → 404 (the OpenAPI lists `/` and `/info` at the root). | Not used. |
| 17 | Trade fee units | [OAS] `taker_fee` / `maker_fee` integers ("omitted if zero"), unit not documented ([LIVE] 50 on a $99 trade). | Kept as raw int64. |
| 18 | `size_decimals` vs `supported_size_decimals` | [DOC] trading page says to use `supported_*`. [LIVE] both equal on all 235 mainnet markets. | `supported_*` used. |
| 19 | Cancel / modify addressing | [DOC] the `index` of cancel / modify is documented as the order index; client order indexes (< 2^48) and exchange order indexes (≥ 2^48) occupy disjoint ranges (lighter-go constants). Not yet confirmed live. | Both accepted in `OrderIndex`; the desk connector cancels by `client_order_index` — verify with `examples/simple-trade`. |
| 20 | `sendTxBatch` atomicity | [DOC] reply is one `code` + a `tx_hash` array, no per-row status; whether the API server accepts part of a batch is not documented. | The desk connector treats any exchange-level batch error as "outcome unknown" (echo + cancel by client id). |
| 21 | Per-market `account_orders` push | [LIVE] `account_all_orders` pushes carry `"channel":"account_all_orders:1"`; the per-market channel `account_orders/{m}/{acc}` is documented but its push channel string (`account_orders:{m}:{acc}` assumed) is not observed yet. | RouteKey normalises ':' → '/'; prefix fallback lookup covers deviations. |

## 2. Endpoints

| | REST | WebSocket | chain id |
|---|---|---|---|
| mainnet | `https://mainnet.zklighter.elliot.ai` | `wss://mainnet.zklighter.elliot.ai/stream` | 304 |
| testnet | `https://testnet.zklighter.elliot.ai` | `wss://testnet.zklighter.elliot.ai/stream` | 300 |

REST prefix `/api/v1/`. Reads are GET; `sendTx` / `sendTxBatch` are POST
`application/x-www-form-urlencoded`; management endpoints are POST JSON.
Every answer is `{"code": N, "message": "…", …}`; 200 = success [OAS].

## 3. Signing [GO]

```
hash    = Poseidon2(plonky2)( chainId, txType, nonce, expiredAt, accountIndex, apiKeyIndex, <fields> )
          if attributes present: hash = Poseidon2( hash[5] ‖ Poseidon2( (t1,v1) … (t4,v4) )[5] )
sig     = Schnorr_ECgFp5( hash, key )        # 80 bytes: s (40 LE) ‖ e (40 LE)
tx_info = encoding/json of the reference struct (keys in struct order, Sig base64,
          "L2TxAttributes": null | {"<type>": value, …})
tx_hash = hex(hash, 40 LE bytes)             # equals the hash returned by sendTx
```

Field layouts per transaction type are in `internal/tx/*.go`; 19 vectors in
`internal/tx/vectors_generated_test.go` (regenerate with `make vectors`).

## 4. Nonces [DOC] [PY]

- Per API key; without `SkipNonce` (attribute 4) the exchange requires
  `new = old + 1`; with it any `old < new < 2^47 - 1`.
- API-level rejection (code ≠ 200) does not consume the nonce; an accepted
  transaction consumes it even when the sequencer later rejects it (edge
  cases: expired transactions).
- [PY] holds the key's lock through the whole send so transactions of one key
  reach the sequencer in order; parallelism = several keys. The SDK does the
  same in sequential mode (`internal/nonce`).
- `nextNonce` (weight 6) returns the next nonce to use.

## 5. Rate limits [DOC]

See `internal/ratelimit/weights.go` for the tables (REST weights, tiers,
Premium sendTx bucket by staked LIT, per-type buckets, WS limits, cooldowns).
No rate-limit headers are returned [LIVE]; the SDK accounts on its own side.

## 6. WebSocket [DOC] [LIVE]

- Keepalive: a frame every < 2 min; `{"type":"ping"}` ↔ `{"type":"pong"}` in
  both directions.
- Subscribe `{"type":"subscribe","channel":"order_book/0"}`; private channels
  add `"auth":"<token>"`; pushes answer on `"channel":"order_book:0"` with
  `type` `subscribed/<name>` (snapshot) or `update/<name>`.
- Observed quirks: `account_orders/{m}/{acc}` answers on `account_orders:{m}`
  [DOC]; the SDK matches by prefix.
- `jsonapi/sendtx` / `jsonapi/sendtxbatch` data: `{"id","tx_type","tx_info":{…}}`
  and `{"id","tx_types":"[…]","tx_infos":"[\"{…}\"]"}` [PY].

## 7. Colocation [DOC]

AWS Tokyo `ap-northeast-1a` (apne1-az4). Premium accounts on top staking tiers
can request direct CloudFront-bypass access from support.
