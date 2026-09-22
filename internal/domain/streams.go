/*
FILE: internal/domain/streams.go

DESCRIPTION:
Unified WebSocket streams of the common layer. One generic helper (watch)
registers a subscription on the shared stream connection and ties its
lifetime to the caller's ctx; thin typed functions define the channel, the
auth requirement and the decoding.

CHANNELS (official WebSocket reference, shapes verified live on 2026-09-22):
  order_book/{market}          snapshot + deltas, begin_nonce continuity
  ticker/{market}              best bid / offer on every nonce change
  market_stats/{market}        mark / index / funding
  trade/{market}               trades and liquidation trades
  candle/{market}/{resolution} current candle, 500 ms batching
  account_all_orders/{acc}     (auth) orders of every market, keyed by market
  account_orders/{m}/{acc}     (auth) orders of one market
  account_all_positions/{acc}  positions keyed by market
  user_stats/{acc}             collateral, leverage, buying power
  account_tx/{acc}             (auth) transactions with their sequencer status

SHARED FEEDS:
Several Watch* calls may target the same channel; the connection subscribes
the exchange once and delivers every push to all of them.

LIFETIME:
The connection belongs to the root Client. Cancelling ctx unsubscribes THIS
subscription only. On EVERY connect of the socket every subscription is
replayed by the connection; onReset is invoked first so stateful consumers
can drop state; the exchange then sends a fresh snapshot ("subscribed/*").

HANDLER CONTRACT (hot path):
  - handlers run sequentially on the connection's read goroutine — they must
    not block;
  - the value passed to a handler is REUSED for the next push of the same
    subscription (slices keep their capacity). Copy what must outlive the call.

ERROR POLICY (same as the sibling SDKs):
A push that fails to decode is logged and dropped; errHandler is NOT called
for it (one malformed frame must not tear a stream down). errHandler receives
only failures of the subscription itself.
*/

package domain

import (
	"context"
	"strconv"

	"github.com/tonymontanov/go-lighter/internal/codec"
	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/internal/ltlog"
	"github.com/tonymontanov/go-lighter/internal/orderbook"
	"github.com/tonymontanov/go-lighter/internal/ws"
	"github.com/tonymontanov/go-lighter/types"
)

// StreamDeps — what a stream needs from the client.
type StreamDeps struct {
	Conn   *ws.Conn
	Engine *engine.Engine
	Logger ltlog.Logger
}

// auth returns the token supplier of private channels.
func (d StreamDeps) auth() func() (string, error) {
	return d.Engine.AuthToken
}

// watch builds a subscription and registers it (see register).
func watch(ctx context.Context, deps StreamDeps, channel string, private bool, handler func(frame []byte, isSnapshot bool), onReset func(), errHandler func(error)) (*ws.Subscription, error) {
	var subscription *ws.Subscription = &ws.Subscription{Channel: channel, Handler: handler, Reset: onReset}
	if private {
		subscription.Auth = deps.auth()
	}
	var err error = register(ctx, deps, subscription, errHandler)
	if err != nil {
		return nil, err
	}
	return subscription, nil
}

// register subscribes and unsubscribes when ctx is done. The subscription
// must be fully built before the call: frames may be dispatched to its
// handler before Subscribe returns.
func register(ctx context.Context, deps StreamDeps, subscription *ws.Subscription, errHandler func(error)) error {
	var err error = deps.Conn.Subscribe(subscription)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	go func() {
		<-ctx.Done()
		// Removes THIS consumer only; the exchange is unsubscribed when the
		// last consumer of the channel leaves. ErrConnClosed after
		// Client.Close is expected and ignored.
		_ = deps.Conn.Unsubscribe(subscription)
	}()
	return nil
}

// dropped logs a push that failed to decode.
func dropped(deps StreamDeps, channel string, err error) {
	deps.Logger.Warn("stream: dropped a malformed push", ltlog.Str("channel", channel), ltlog.Err(err))
}

// marketChannel builds "<name>/<market id>".
func marketChannel(name string, marketID int16) string {
	return name + "/" + strconv.FormatInt(int64(marketID), 10)
}

// accountChannel builds "<name>/<account index>".
func accountChannel(name string, accountIndex int64) string {
	return name + "/" + strconv.FormatInt(accountIndex, 10)
}

// orderBookFrame — order_book push.
type orderBookFrame struct {
	Offset          int64 `json:"offset"`
	LastUpdatedAtUs int64 `json:"last_updated_at"`
	TimestampMs     int64 `json:"timestamp"`
	OrderBook       struct {
		Asks            []types.OrderBookLevel `json:"asks"`
		Bids            []types.OrderBookLevel `json:"bids"`
		Offset          int64                  `json:"offset"`
		Nonce           int64                  `json:"nonce"`
		LastUpdatedAtUs int64                  `json:"last_updated_at"`
		BeginNonce      int64                  `json:"begin_nonce"`
	} `json:"order_book"`
}

// toUpdate fills a public update from the frame (slices are shared).
func (f *orderBookFrame) toUpdate(marketID int16, isSnapshot bool, out *types.OrderBookUpdate) {
	out.MarketID = marketID
	out.IsSnapshot = isSnapshot
	out.Asks = f.OrderBook.Asks
	out.Bids = f.OrderBook.Bids
	out.Offset = f.OrderBook.Offset
	out.Nonce = f.OrderBook.Nonce
	out.BeginNonce = f.OrderBook.BeginNonce
	out.LastUpdatedAtUs = f.OrderBook.LastUpdatedAtUs
	out.TimestampMs = f.TimestampMs
}

/*
WatchOrderBookUpdates streams the raw order_book pushes (snapshot first,
then deltas). Consumers that want a maintained book use WatchOrderBook.
*/
func WatchOrderBookUpdates(ctx context.Context, deps StreamDeps, p *Profile, symbol string, handler func(*types.OrderBookUpdate), onReset func(), errHandler func(error)) error {
	const operation string = "WatchOrderBookUpdates"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var frame orderBookFrame
	var update types.OrderBookUpdate
	_, err = watch(ctx, deps, marketChannel("order_book", info.MarketID), false, func(data []byte, isSnapshot bool) {
		frame.OrderBook.Asks = frame.OrderBook.Asks[:0]
		frame.OrderBook.Bids = frame.OrderBook.Bids[:0]
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "order_book", decodeErr)
			return
		}
		frame.toUpdate(info.MarketID, isSnapshot, &update)
		handler(&update)
	}, onReset, errHandler)
	return err
}

/*
WatchOrderBook maintains a local book (internal/orderbook) from the
order_book channel and calls handler with the full book after every applied
push. On a continuity gap (begin_nonce != last nonce) the SDK resubscribes
to obtain a fresh snapshot; onGap, when set, is called first. Nothing is
delivered until the snapshot after a gap arrives.
*/
func WatchOrderBook(ctx context.Context, deps StreamDeps, p *Profile, symbol string, maxDepth int, handler func(*types.OrderBook), onGap func(), errHandler func(error)) error {
	const operation string = "WatchOrderBook"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var book *orderbook.Book = orderbook.New(info.MarketID, maxDepth)
	var frame orderBookFrame
	var update types.OrderBookUpdate
	// resubscribing / book are touched only on the connection goroutine
	// (handler and Reset both run there).
	var resubscribing bool
	var subscription *ws.Subscription = &ws.Subscription{Channel: marketChannel("order_book", info.MarketID)}

	// resubscribe drops the exchange subscription and re-registers it: the
	// exchange answers a fresh subscription with a full snapshot. It must not
	// run on the connection goroutine (it writes to the socket), hence the
	// goroutine. Built BEFORE registering so the handler never sees it unset.
	var resubscribe func() = func() {
		go func() {
			if ctx.Err() != nil {
				return
			}
			_ = deps.Conn.Unsubscribe(subscription)
			if subscribeErr := deps.Conn.Subscribe(subscription); subscribeErr != nil && errHandler != nil {
				errHandler(subscribeErr)
			}
		}()
	}
	subscription.Handler = func(data []byte, isSnapshot bool) {
		if resubscribing && !isSnapshot {
			return
		}
		frame.OrderBook.Asks = frame.OrderBook.Asks[:0]
		frame.OrderBook.Bids = frame.OrderBook.Bids[:0]
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "order_book", decodeErr)
			return
		}
		frame.toUpdate(info.MarketID, isSnapshot, &update)
		if isSnapshot {
			resubscribing = false
			book.ApplySnapshot(&update)
			handler(book.View())
			return
		}
		if !book.ApplyUpdate(&update) {
			deps.Logger.Warn("stream: order book gap, resubscribing", ltlog.Str("symbol", symbol), ltlog.Int("begin_nonce", update.BeginNonce), ltlog.Int("last_nonce", book.View().Nonce))
			if onGap != nil {
				onGap()
			}
			resubscribing = true
			resubscribe()
			return
		}
		handler(book.View())
	}
	subscription.Reset = func() { book.Reset(); resubscribing = false }
	return register(ctx, deps, subscription, errHandler)
}

// tickerFrame — ticker push.
type tickerFrame struct {
	LastUpdatedAtUs int64 `json:"last_updated_at"`
	Nonce           int64 `json:"nonce"`
	TimestampMs     int64 `json:"timestamp"`
	Ticker          struct {
		Symbol          string               `json:"s"`
		Ask             types.OrderBookLevel `json:"a"`
		Bid             types.OrderBookLevel `json:"b"`
		LastUpdatedAtUs int64                `json:"last_updated_at"`
	} `json:"ticker"`
}

// WatchTicker streams the best bid / offer of a market.
func WatchTicker(ctx context.Context, deps StreamDeps, p *Profile, symbol string, handler func(*types.Ticker), errHandler func(error)) error {
	const operation string = "WatchTicker"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var frame tickerFrame
	var ticker types.Ticker
	_, err = watch(ctx, deps, marketChannel("ticker", info.MarketID), false, func(data []byte, isSnapshot bool) {
		frame.Ticker.Ask = types.OrderBookLevel{}
		frame.Ticker.Bid = types.OrderBookLevel{}
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "ticker", decodeErr)
			return
		}
		ticker.MarketID = info.MarketID
		ticker.Symbol = frame.Ticker.Symbol
		ticker.Ask = frame.Ticker.Ask
		ticker.Bid = frame.Ticker.Bid
		ticker.Nonce = frame.Nonce
		ticker.LastUpdatedAtUs = frame.LastUpdatedAtUs
		if ticker.LastUpdatedAtUs == 0 {
			ticker.LastUpdatedAtUs = frame.Ticker.LastUpdatedAtUs
		}
		ticker.TimestampMs = frame.TimestampMs
		handler(&ticker)
	}, nil, errHandler)
	return err
}

// marketStatsFrame — market_stats push.
type marketStatsFrame struct {
	MarketStats types.MarketStats `json:"market_stats"`
	TimestampMs int64             `json:"timestamp"`
}

// WatchMarketStats streams mark / index prices and funding of a market.
func WatchMarketStats(ctx context.Context, deps StreamDeps, p *Profile, symbol string, handler func(*types.MarketStats, int64), errHandler func(error)) error {
	const operation string = "WatchMarketStats"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var frame marketStatsFrame
	_, err = watch(ctx, deps, marketChannel("market_stats", info.MarketID), false, func(data []byte, isSnapshot bool) {
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "market_stats", decodeErr)
			return
		}
		handler(&frame.MarketStats, frame.TimestampMs)
	}, nil, errHandler)
	return err
}

// tradesFrame — trade push.
type tradesFrame struct {
	Trades            []types.Trade `json:"trades"`
	LiquidationTrades []types.Trade `json:"liquidation_trades"`
	Nonce             int64         `json:"nonce"`
}

// TradesPush — one push of the trade channel. Slices are reused between pushes.
type TradesPush struct {
	Trades            []types.Trade
	LiquidationTrades []types.Trade
	Nonce             int64
}

// WatchTrades streams public trades of a market.
func WatchTrades(ctx context.Context, deps StreamDeps, p *Profile, symbol string, handler func(*TradesPush), errHandler func(error)) error {
	const operation string = "WatchTrades"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var frame tradesFrame
	var push TradesPush
	_, err = watch(ctx, deps, marketChannel("trade", info.MarketID), false, func(data []byte, isSnapshot bool) {
		frame.Trades = frame.Trades[:0]
		frame.LiquidationTrades = frame.LiquidationTrades[:0]
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "trade", decodeErr)
			return
		}
		push.Trades = frame.Trades
		push.LiquidationTrades = frame.LiquidationTrades
		push.Nonce = frame.Nonce
		handler(&push)
	}, nil, errHandler)
	return err
}

// candlesFrame — candle push.
type candlesFrame struct {
	Candles     []types.Candle `json:"candles"`
	TimestampMs int64          `json:"timestamp"`
}

// WatchCandles streams the current candle(s) of a market / resolution. Up to
// two candles arrive when a period closes: the closed one first.
func WatchCandles(ctx context.Context, deps StreamDeps, p *Profile, symbol string, resolution string, handler func([]types.Candle), errHandler func(error)) error {
	const operation string = "WatchCandles"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err == nil && !types.ValidResolution(resolution) {
		err = p.invalid(operation, "invalid resolution "+resolution)
	}
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var frame candlesFrame
	_, err = watch(ctx, deps, marketChannel("candle", info.MarketID)+"/"+resolution, false, func(data []byte, isSnapshot bool) {
		frame.Candles = frame.Candles[:0]
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "candle", decodeErr)
			return
		}
		handler(frame.Candles)
	}, nil, errHandler)
	return err
}

// ordersFrame — account_all_orders / account_orders push: orders keyed by market id.
type ordersFrame struct {
	Orders map[string][]types.Order `json:"orders"`
	Nonce  int64                    `json:"nonce"`
}

// OrdersPush — one push of an orders channel: the orders of the section's
// markets, flattened. IsSnapshot marks the subscription snapshot.
type OrdersPush struct {
	Orders     []types.Order
	IsSnapshot bool
	Nonce      int64
}

/*
WatchOrders streams order updates. symbol == "" subscribes to
account_all_orders (every market, filtered to the section), otherwise to
account_orders/{market}/{account}. Both are private (auth token).
onReset fires on every (re)connect: updates may have been missed while the
socket was down, so consumers should re-seed from ActiveOrders.
*/
func WatchOrders(ctx context.Context, deps StreamDeps, p *Profile, symbol string, handler func(*OrdersPush), onReset func(), errHandler func(error)) error {
	const operation string = "WatchOrders"
	var err error = ensureMarkets(ctx, p)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var channel string
	if symbol == "" {
		channel = accountChannel("account_all_orders", deps.Engine.AccountIndex())
	} else {
		var info *types.MarketInfo
		info, err = p.resolve(ctx, operation, symbol)
		if err != nil {
			if errHandler != nil {
				errHandler(err)
			}
			return err
		}
		channel = marketChannel("account_orders", info.MarketID) + "/" + strconv.FormatInt(deps.Engine.AccountIndex(), 10)
	}
	var frame ordersFrame
	var push OrdersPush
	_, err = watch(ctx, deps, channel, true, func(data []byte, isSnapshot bool) {
		frame.Orders = nil
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "orders", decodeErr)
			return
		}
		push.Orders = push.Orders[:0]
		push.IsSnapshot = isSnapshot
		push.Nonce = frame.Nonce
		var list []types.Order
		for _, list = range frame.Orders {
			var i int
			for i = 0; i < len(list); i++ {
				if p.Owns(list[i].MarketIndex) {
					push.Orders = append(push.Orders, list[i])
				}
			}
		}
		handler(&push)
	}, onReset, errHandler)
	return err
}

// positionsFrame — account_all_positions push: positions keyed by market id.
type positionsFrame struct {
	Positions map[string]types.Position `json:"positions"`
}

// PositionsPush — one push of the positions channel (section's markets only).
type PositionsPush struct {
	Positions  []types.Position
	IsSnapshot bool
}

// WatchPositions streams position updates of the account (public channel:
// no auth token). onReset fires on every (re)connect.
func WatchPositions(ctx context.Context, deps StreamDeps, p *Profile, handler func(*PositionsPush), onReset func(), errHandler func(error)) error {
	var err error = ensureMarkets(ctx, p)
	if err != nil {
		if errHandler != nil {
			errHandler(err)
		}
		return err
	}
	var frame positionsFrame
	var push PositionsPush
	_, err = watch(ctx, deps, accountChannel("account_all_positions", deps.Engine.AccountIndex()), false, func(data []byte, isSnapshot bool) {
		frame.Positions = nil
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "account_all_positions", decodeErr)
			return
		}
		push.Positions = push.Positions[:0]
		push.IsSnapshot = isSnapshot
		var position types.Position
		for _, position = range frame.Positions {
			if p.Owns(position.MarketID) {
				push.Positions = append(push.Positions, position)
			}
		}
		handler(&push)
	}, onReset, errHandler)
	return err
}

// accountStatsFrame — user_stats push.
type accountStatsFrame struct {
	Stats       types.AccountStats `json:"stats"`
	TimestampMs int64              `json:"timestamp"`
}

// WatchAccountStats streams collateral / leverage / buying power (user_stats).
func WatchAccountStats(ctx context.Context, deps StreamDeps, handler func(*types.AccountStats), errHandler func(error)) error {
	var frame accountStatsFrame
	var err error
	_, err = watch(ctx, deps, accountChannel("user_stats", deps.Engine.AccountIndex()), false, func(data []byte, isSnapshot bool) {
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "user_stats", decodeErr)
			return
		}
		frame.Stats.TimestampMs = frame.TimestampMs
		handler(&frame.Stats)
	}, nil, errHandler)
	return err
}

// transactionsFrame — account_tx push.
type transactionsFrame struct {
	Txs []types.Transaction `json:"txs"`
}

/*
WatchTransactions streams the account's transactions with their sequencer
status (account_tx, auth): the second confirmation level after the sendTx
receipt. Every push carries a batch of transactions; the outcome of each is
handed to handler.
*/
func WatchTransactions(ctx context.Context, deps StreamDeps, handler func(*types.TxOutcome), onReset func(), errHandler func(error)) error {
	var frame transactionsFrame
	var outcome types.TxOutcome
	var err error
	_, err = watch(ctx, deps, accountChannel("account_tx", deps.Engine.AccountIndex()), true, func(data []byte, isSnapshot bool) {
		frame.Txs = frame.Txs[:0]
		if decodeErr := codec.Unmarshal(data, &frame); decodeErr != nil {
			dropped(deps, "account_tx", decodeErr)
			return
		}
		var i int
		for i = 0; i < len(frame.Txs); i++ {
			outcome.Hash = frame.Txs[i].Hash
			outcome.Status = frame.Txs[i].Status
			outcome.AppError = frame.Txs[i].AppError()
			outcome.ExecutedAtMs = frame.Txs[i].ExecutedAtMs
			outcome.Transaction = frame.Txs[i]
			handler(&outcome)
		}
	}, onReset, errHandler)
	return err
}
