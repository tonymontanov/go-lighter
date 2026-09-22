# go-lighter

Low-latency Go SDK for the [Lighter](https://lighter.xyz) exchange (zkLighter:
a perpetuals DEX on its own zk-rollup with an on-chain order book and
sequencer-side matching). Built for HFT / market-making desks; a sibling of
go-okx, go-bybit and go-hyperliquid, consumed by the desk's trading core.

[![ci](https://github.com/tonymontanov/go-lighter/actions/workflows/ci.yml/badge.svg)](https://github.com/tonymontanov/go-lighter/actions)

## Status

| Milestone | Status | Verification |
|---|---|---|
| Signing & transactions (types 14, 15, 16, 17, 20, 28, 29) | ✅ | 19 vectors byte-identical to `lighter-go` v1.0.10 |
| REST + WebSocket transports, rate-limit accounting | ✅ | unit tests, live frame shapes |
| `perpetuals` section: Trading / Account / MarketData / Stream | ✅ | contract tests on an in-process mock |
| Keyless live smoke (`make smoke`) | ✅ | mainnet, 2026-09-23 |
| Live order path (`examples/simple-trade`) | ⏳ | run by the owner (real funds) |
| v2.0: TP/SL, TWAP, grouped orders, key pool, WS trading | 📋 | trigger types and grouped-order transactions already signed and vector-tested |
| v2.5: bridge, pools, account management, history, spot | 📋 | |

## Dependencies

`gorilla/websocket`, `json-iterator/go`, `shopspring/decimal`,
`elliottech/poseidon_crypto` (Poseidon2 + ECgFp5 Schnorr — the exact
primitives of the official signer). Pure Go, builds with `CGO_ENABLED=0`.
The official `lighter-go` is used only by the vector generator
(`scripts/gen-vectors`, a separate module).

## Structure

```
lighter        root: Client, Config, errors, logger, metrics, rate-limit events, TxTracker
types          Fixed / Precision, enums, requests, views (market, order, account, tx)
perpetuals     section "Perpetuals": Trading() Account() MarketData() Stream()
internal/      signing, tx, nonce, ratelimit, rest, ws, markets, orderbook, engine, domain
examples/      market-data (keyless), simple-trade (gated: real funds)
docs/          ToR (RU / EN), API-NOTES.md (verified facts and discrepancies), DEPLOY.md
```

Two-layer architecture: every request is implemented once in the common layer
(`internal/domain` + `internal/engine`), each section adds only its specifics
(market_type filter, market registry). Sections never import each other.

## Quick start

```go
package main

import (
    "context"
    "os"

    lighter "github.com/tonymontanov/go-lighter"
    "github.com/tonymontanov/go-lighter/perpetuals"
    "github.com/tonymontanov/go-lighter/types"
)

func main() {
    var cfg lighter.Config = lighter.DefaultConfig()
    cfg.AccountIndex = 123          // your account (or sub-account) index
    cfg.APIKeyIndex = 3             // index of the API key below (2..254)
    cfg.PrivateKey = os.Getenv("LIGHTER_PERPETUALS_SECRET_KEY")

    client, err := lighter.NewClient(cfg)
    if err != nil {
        panic(err)
    }
    defer client.Close()
    var perps *perpetuals.Client = perpetuals.NewClient(client)
    var ctx context.Context = context.Background()

    // Post-only buy: prices and sizes are types.Fixed on the market's grid.
    receipt, err := perps.Trading().CreateOrder(ctx, types.CreateOrderRequest{
        Symbol:           "ETH",
        ClientOrderIndex: 1001,
        IsAsk:            false,
        Price:            types.MustParseFixed("2000.00"),
        Size:             types.MustParseFixed("0.0100"),
        Type:             types.OrderTypeLimit,
        TimeInForce:      types.TimeInForcePostOnly,
    }, types.SendOptions{})
    _ = receipt // accepted by the API server; the sequencer confirms later
    _ = err
}
```

### Two confirmation levels

`sendTx` answers with a `types.TxReceipt`: the API server accepted the
transaction (syntax and signature fine, nonce consumed). Execution happens in
the sequencer and is reported on the `account_tx` stream as a
`types.TxOutcome`. `lighter.TxTracker` correlates the two:

```go
var tracker *lighter.TxTracker = lighter.NewTxTracker(4096)
_ = perps.Stream().WatchTransactions(ctx, tracker.Observe, nil, errHandler)
receipt, _ := perps.Trading().CreateOrder(ctx, req, types.SendOptions{})
outcome, _ := tracker.Await(ctx, receipt.TxHash)   // outcome.Executed() / outcome.Failed()
```

### Nonces and keys

Nonces are tracked per API key. In the default sequential mode the SDK
serialises the round trip per key (the strategy of the official SDKs) and
rolls back / resynchronises on rejections; several keys (`Config.ExtraKeys`,
`SendOptions.APIKeyIndex`) give independent nonce lanes. `NonceModeSkip`
uses monotonic millisecond nonces with the `SkipNonce` attribute.

### Streams

```go
_ = perps.Stream().WatchOrderBook(ctx, "ETH", 20, func(b *types.OrderBook) {
    // full book after every applied delta; gaps trigger an automatic resubscribe
}, nil, errHandler)
_ = perps.Stream().WatchTicker(ctx, "ETH", func(t *types.Ticker) { /* best bid / ask */ }, errHandler)
_ = perps.Stream().WatchOrders(ctx, "", func(p *perpetuals.OrdersPush) { /* order updates */ }, nil, errHandler)
```

Handlers run on the connection goroutine and receive reused values: copy
what must outlive the call, never block.

## Examples

```bash
make smoke                                   # keyless market data, mainnet
cp .env.example .env                         # fill in the credentials
LIGHTER_ALLOW_LIVE=1 ./scripts/run.sh ./examples/simple-trade   # REAL FUNDS
```

## Development

```bash
make check      # gofmt, vet, race tests
make bench      # hot-path benchmarks (allocs/op must not grow)
make vectors    # regenerate signing vectors with lighter-go
```

Hot-path numbers on Apple M4 Pro: Poseidon2 transaction hash 3.3 µs / 0
allocs, tx_info JSON 120 ns / 0 allocs, sendTx form 430 ns / 0 allocs, order
book delta 64 ns / 0 allocs; the Schnorr signature of the crypto library is
~200 µs / 69 allocs (an allocation-free scalar multiplication is a roadmap item).

## Code style

English comments everywhere; file header `FILE / DESCRIPTION / MAIN FUNCTIONS /
DEPENDENCIES`; explicit `var x T = ...` declarations; camelCase locals;
kebab-case file names; zero allocations on hot paths; validation before
network; no invented API fields (see `docs/API-NOTES.md`).

## License

Apache 2.0 — see `LICENSE`.
