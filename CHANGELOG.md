# Changelog

All notable changes to this project are documented here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versions follow
[Semantic Versioning](https://semver.org/).

## [Unreleased] — v1.0.0 (Perpetuals)

### Added
- Common layer: root `lighter` package (`Client`, `Config`, error / logger /
  metrics re-exports, rate-limit events, `TxTracker`); `types` (`Fixed`,
  `Precision`, enums, requests, market / order / account / transaction views).
- Signing: allocation-free Poseidon2 sponge, Schnorr over ECgFp5 via
  `elliottech/poseidon_crypto`, auth tokens; transactions createOrder,
  cancelOrder, cancelAllOrders, modifyOrder, updateLeverage,
  createGroupedOrders, updateMargin with the reference validation rules and
  all eight L2 attributes. 19 vectors generated with the official
  `lighter-go` v1.0.10 — hash, signature (fixed k) and tx_info byte-identical.
- Nonce lanes per API key: sequential (`+1`, serialised sends, rollback on
  API rejection, resync on nonce errors / transport failures) and skip mode
  (monotonic millisecond nonces).
- Transports: REST (`/api/v1`, form-encoded sendTx / sendTxBatch, envelope
  mapping), supervised WebSocket (`/stream`: keepalive both ways,
  per-subscription auth tokens, resubscribe + reset, jsonapi posts by id).
- SDK-side rate-limit accounting: endpoint weights, tiers, Premium sendTx
  bucket by staked LIT, per-transaction-type buckets, 429 / 405 cooldowns,
  observer events, optional local fail-fast.
- Market registry (symbol ↔ market id ↔ grid) with background refresh;
  order-book engine (snapshot + deltas, `begin_nonce` continuity,
  automatic resubscribe on gaps).
- `perpetuals` section: Trading (create / modify / cancel + batches,
  cancel-all with market scope and dead man's switch, forgotten orders,
  active / inactive / by-client-index reads), Account (account, positions,
  balance, leverage, isolated margin, close position, API key check,
  limits, trades), MarketData (markets, details, book, trades, candles,
  funding rates, slippage price), Stream (order book, ticker, market stats,
  trades, candles, orders, positions, account stats, transactions).
- Examples `market-data` (keyless) and `simple-trade` (gated, live funds).
- Tooling: Makefile, golangci-lint config, GitHub Actions (fmt, vet, race,
  cgo-free build, lint), vector generator module.

[Unreleased]: https://github.com/tonymontanov/go-lighter
