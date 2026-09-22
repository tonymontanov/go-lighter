/*
Package lighter is a low-latency Go SDK for the Lighter exchange (zkLighter:
a perpetuals DEX on its own zk-rollup with an on-chain order book and
sequencer-side matching).

# Architecture

The SDK follows the two-layer principle shared by the sibling SDKs
(go-okx, go-bybit, go-hyperliquid): ONE common layer implements every
request, and each exchange section is a thin specialisation that contributes
only its own specifics. Sections never import each other.

	lighter (root)     public face of the common layer: Client, Config, errors,
	                   logger, metrics, rate-limit events, TxTracker
	types              layer-1 public types: Fixed, Precision, enums, requests,
	                   market / order / account / transaction views
	perpetuals         section "Perpetuals" (market_type "perp"):
	                   Trading / Account / MarketData / Stream

# Two confirmation levels

Every write is a signed L2 transaction sent with sendTx / sendTxBatch. The
API server answers with a receipt (types.TxReceipt) that only means the
transaction was accepted and forwarded; the sequencer executes it later and
reports the outcome on the account_tx WebSocket channel (types.TxOutcome).
TxTracker correlates the two.

# Quick start

	var cfg lighter.Config = lighter.DefaultConfig()
	cfg.AccountIndex = 123
	cfg.APIKeyIndex = 3
	cfg.PrivateKey = os.Getenv("LIGHTER_PERPETUALS_SECRET_KEY")

	client, err := lighter.NewClient(cfg)
	perps := perpetuals.NewClient(client)
	receipt, err := perps.Trading().CreateOrder(ctx, types.CreateOrderRequest{
		Symbol: "ETH", ClientOrderIndex: 1, IsAsk: false,
		Price: types.MustParseFixed("2000.00"), Size: types.MustParseFixed("0.01"),
		Type: types.OrderTypeLimit, TimeInForce: types.TimeInForcePostOnly,
	}, types.SendOptions{})

Prices and sizes are types.Fixed (int64 with 8 decimals) and must lie on the
market's grid (types.Precision, from orderBookDetails); the SDK never rounds
silently.
*/
package lighter
