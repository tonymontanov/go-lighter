/*
FILE: perpetuals/stream.go

DESCRIPTION:
WebSocket streams of the Perpetuals section. Every method is the unified
stream of the common layer plus the section profile; the connection is the
shared one of the root client (several consumers of one channel = one
exchange subscription).

CONTRACT (see internal/domain/streams.go):
  - handlers run on the connection's read goroutine and must not block;
  - the value passed to a handler is reused for the next push — copy what
    must outlive the call;
  - cancelling ctx unsubscribes this consumer; the stream ends silently;
  - onReset (where offered) fires on every (re)connect before the fresh
    snapshot: stateful consumers drop their state there.
*/

package perpetuals

import (
	"context"

	"github.com/tonymontanov/go-lighter/internal/domain"
	"github.com/tonymontanov/go-lighter/types"
)

// StreamClient — WebSocket streams sub-client.
type StreamClient struct {
	c *Client
}

// IsConnected reports whether the shared stream socket is live right now.
func (s *StreamClient) IsConnected() bool { return s.c.parent.StreamConnected() }

// WarmUp connects the stream socket and waits until it is live.
func (s *StreamClient) WarmUp(ctx context.Context) error { return s.c.parent.WarmUpStream(ctx) }

// WatchOrderBook maintains a local book from the order_book channel
// (snapshot + deltas with nonce continuity) and delivers the full book after
// every applied push. maxDepth <= 0 keeps every level. On a gap the SDK
// resubscribes; onGap (optional) is called first.
func (s *StreamClient) WatchOrderBook(ctx context.Context, symbol string, maxDepth int, handler func(*types.OrderBook), onGap func(), errHandler func(error)) error {
	return domain.WatchOrderBook(ctx, s.c.streamDeps(), s.c.prof(), symbol, maxDepth, handler, onGap, errHandler)
}

// WatchOrderBookUpdates streams the raw order_book pushes (snapshot, then deltas).
func (s *StreamClient) WatchOrderBookUpdates(ctx context.Context, symbol string, handler func(*types.OrderBookUpdate), onReset func(), errHandler func(error)) error {
	return domain.WatchOrderBookUpdates(ctx, s.c.streamDeps(), s.c.prof(), symbol, handler, onReset, errHandler)
}

// WatchTicker streams the best bid / offer of a market.
func (s *StreamClient) WatchTicker(ctx context.Context, symbol string, handler func(*types.Ticker), errHandler func(error)) error {
	return domain.WatchTicker(ctx, s.c.streamDeps(), s.c.prof(), symbol, handler, errHandler)
}

// WatchMarketStats streams mark / index prices and funding of a market; the
// second argument of the handler is the frame timestamp (unix ms).
func (s *StreamClient) WatchMarketStats(ctx context.Context, symbol string, handler func(*types.MarketStats, int64), errHandler func(error)) error {
	return domain.WatchMarketStats(ctx, s.c.streamDeps(), s.c.prof(), symbol, handler, errHandler)
}

// WatchTrades streams public trades (and liquidation trades) of a market.
func (s *StreamClient) WatchTrades(ctx context.Context, symbol string, handler func(*domain.TradesPush), errHandler func(error)) error {
	return domain.WatchTrades(ctx, s.c.streamDeps(), s.c.prof(), symbol, handler, errHandler)
}

// WatchCandles streams the current candle(s) of a market / resolution.
func (s *StreamClient) WatchCandles(ctx context.Context, symbol string, resolution string, handler func([]types.Candle), errHandler func(error)) error {
	return domain.WatchCandles(ctx, s.c.streamDeps(), s.c.prof(), symbol, resolution, handler, errHandler)
}

// WatchOrders streams order updates of the account (private). symbol == ""
// covers every perp market. onReset fires on every (re)connect.
func (s *StreamClient) WatchOrders(ctx context.Context, symbol string, handler func(*domain.OrdersPush), onReset func(), errHandler func(error)) error {
	return domain.WatchOrders(ctx, s.c.streamDeps(), s.c.prof(), symbol, handler, onReset, errHandler)
}

// WatchPositions streams position updates of the perp markets.
func (s *StreamClient) WatchPositions(ctx context.Context, handler func(*domain.PositionsPush), onReset func(), errHandler func(error)) error {
	return domain.WatchPositions(ctx, s.c.streamDeps(), s.c.prof(), handler, onReset, errHandler)
}

// WatchAccountStats streams collateral / leverage / buying power (user_stats).
func (s *StreamClient) WatchAccountStats(ctx context.Context, handler func(*types.AccountStats), errHandler func(error)) error {
	return domain.WatchAccountStats(ctx, s.c.streamDeps(), handler, errHandler)
}

// WatchTransactions streams the sequencer outcome of the account's
// transactions (account_tx, private) — feed it to lighter.TxTracker.Observe.
func (s *StreamClient) WatchTransactions(ctx context.Context, handler func(*types.TxOutcome), onReset func(), errHandler func(error)) error {
	return domain.WatchTransactions(ctx, s.c.streamDeps(), handler, onReset, errHandler)
}

// Re-exported push types of the streams.
type (
	// TradesPush — one push of the trade channel.
	TradesPush = domain.TradesPush
	// OrdersPush — one push of an orders channel.
	OrdersPush = domain.OrdersPush
	// PositionsPush — one push of the positions channel.
	PositionsPush = domain.PositionsPush
	// InactiveOrdersPage — one page of inactive orders.
	InactiveOrdersPage = domain.InactiveOrdersPage
)
