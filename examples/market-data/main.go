/*
FILE: examples/market-data/main.go

DESCRIPTION:
Keyless, read-only smoke test of the Perpetuals section against the public
API (mainnet by default, testnet with LIGHTER_TESTNET=1):

 1. load market metadata (orderBookDetails) and print a few markets;
 2. read the live details, an order-book snapshot, recent trades and candles
    of one market over REST;
 3. subscribe to the ticker, the maintained order book, market stats and
    trades over WebSocket for a few seconds and print what arrives;
 4. print the SDK-side rate-limit accounting.

No credentials are needed and nothing is sent to the exchange.

USAGE:
    go run ./examples/market-data                 # mainnet
    LIGHTER_TESTNET=1 go run ./examples/market-data
    LIGHTER_SYMBOL=BTC go run ./examples/market-data
*/

package main

import (
	"context"
	"fmt"
	"os"
	"time"

	lighter "github.com/tonymontanov/go-lighter"
	"github.com/tonymontanov/go-lighter/perpetuals"
	"github.com/tonymontanov/go-lighter/types"
)

func main() {
	var err error = run()
	if err != nil {
		fmt.Println("FAILED:", err)
		os.Exit(1)
	}
}

// step wraps an error with the name of the failed step.
func step(name string, err error) error {
	return fmt.Errorf("at %s: %w", name, err)
}

// run holds the whole example so that deferred cleanups run before exit.
func run() error {
	var symbol string = os.Getenv("LIGHTER_SYMBOL")
	if symbol == "" {
		symbol = "ETH"
	}
	var cfg lighter.Config = lighter.DefaultConfig()
	cfg.Testnet = os.Getenv("LIGHTER_TESTNET") == "1"
	cfg.RateLimitEventObserver = func(e lighter.RateLimitEvent) {
		fmt.Printf("  [rate] %-20s status=%d weight=%-3d used=%d/%d requests=%d/%d\n", e.Endpoint, e.HTTPStatus, e.Weight, e.UsedWeight, e.WeightLimit, e.UsedRequests, e.RequestLimit)
	}

	var client *lighter.Client
	var err error
	client, err = lighter.NewClient(cfg)
	if err != nil {
		return step("NewClient", err)
	}
	defer func() { _ = client.Close() }()
	fmt.Printf("network: %s (chain id %d)\n", client.Config().REST.BaseURL, client.Config().ChainID)

	var perps *perpetuals.Client = perpetuals.NewClient(client)
	var ctx context.Context
	var cancel context.CancelFunc
	ctx, cancel = context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// 1. Metadata.
	var markets []types.MarketInfo
	markets, err = perps.MarketData().GetMarkets(ctx)
	if err != nil {
		return step("GetMarkets", err)
	}
	fmt.Printf("perp markets: %d\n", len(markets))
	var i int
	for i = 0; i < len(markets) && i < 5; i++ {
		var m *types.MarketInfo = &markets[i]
		fmt.Printf("  %-8s id=%-4d %-8s tick=%s lot=%s minBase=%s minQuote=%s maxLev=%d\n",
			m.Symbol, m.MarketID, m.Status, m.Precision.TickSize(), m.Precision.LotSize(), m.MinBaseAmount, m.MinQuoteAmount, m.MaxLeverage())
	}

	// 2. REST reads of one market.
	var details types.MarketDetails
	details, err = perps.MarketData().GetMarketDetails(ctx, symbol)
	if err != nil {
		return step("GetMarketDetails", err)
	}
	fmt.Printf("%s: last=%s mark=%s index=%s OI=%s dailyVolume=%s\n", symbol, details.LastTradePrice, details.MarkPrice, details.IndexPrice, details.OpenInterest, details.DailyQuoteTokenVolume)

	var book types.OrderBook
	book, err = perps.MarketData().GetOrderBook(ctx, symbol, 50)
	if err != nil {
		return step("GetOrderBook", err)
	}
	if book.BestBid() != nil && book.BestAsk() != nil {
		fmt.Printf("book: bid %s x %s | ask %s x %s (%d / %d levels)\n", book.BestBid().Price, book.BestBid().Size, book.BestAsk().Price, book.BestAsk().Size, len(book.Bids), len(book.Asks))
	}

	var trades []types.Trade
	trades, err = perps.MarketData().GetRecentTrades(ctx, symbol, 3)
	if err != nil {
		return step("GetRecentTrades", err)
	}
	for i = 0; i < len(trades); i++ {
		fmt.Printf("trade: id=%d %s x %s makerAsk=%v t=%d\n", trades[i].TradeID, trades[i].Price, trades[i].Size, trades[i].IsMakerAsk, trades[i].TimestampMs)
	}

	var now int64 = time.Now().UnixMilli()
	var candles []types.Candle
	candles, err = perps.MarketData().GetCandles(ctx, symbol, types.Resolution1m, now-10*60*1000, now, 3)
	if err != nil {
		return step("GetCandles", err)
	}
	for i = 0; i < len(candles); i++ {
		fmt.Printf("candle: t=%d o=%s h=%s l=%s c=%s v=%s\n", candles[i].OpenTimeMs, candles[i].Open, candles[i].High, candles[i].Low, candles[i].Close, candles[i].BaseVolume)
	}

	// 3. Streams.
	var streamCtx context.Context
	var stopStreams context.CancelFunc
	streamCtx, stopStreams = context.WithTimeout(ctx, 8*time.Second)
	defer stopStreams()
	var errHandler func(error) = func(streamErr error) { fmt.Println("  [stream error]", streamErr) }

	var tickers int
	err = perps.Stream().WatchTicker(streamCtx, symbol, func(t *types.Ticker) {
		tickers++
		if tickers <= 3 {
			fmt.Printf("  [ticker] bid %s x %s | ask %s x %s nonce=%d\n", t.Bid.Price, t.Bid.Size, t.Ask.Price, t.Ask.Size, t.Nonce)
		}
	}, errHandler)
	if err != nil {
		return step("WatchTicker", err)
	}
	var books int
	err = perps.Stream().WatchOrderBook(streamCtx, symbol, 20, func(b *types.OrderBook) {
		books++
		if books <= 3 {
			fmt.Printf("  [book] nonce=%d bid %s | ask %s (%d/%d)\n", b.Nonce, b.BestBid().Price, b.BestAsk().Price, len(b.Bids), len(b.Asks))
		}
	}, func() { fmt.Println("  [book] gap → resubscribe") }, errHandler)
	if err != nil {
		return step("WatchOrderBook", err)
	}
	var stats int
	err = perps.Stream().WatchMarketStats(streamCtx, symbol, func(s *types.MarketStats, tsMs int64) {
		stats++
		if stats <= 2 {
			fmt.Printf("  [stats] mark=%s index=%s funding=%s next=%s\n", s.MarkPrice, s.IndexPrice, s.FundingRate, s.CurrentFundingRate)
		}
	}, errHandler)
	if err != nil {
		return step("WatchMarketStats", err)
	}
	var tradePushes int
	err = perps.Stream().WatchTrades(streamCtx, symbol, func(push *perpetuals.TradesPush) {
		tradePushes++
		if tradePushes <= 2 && len(push.Trades) > 0 {
			fmt.Printf("  [trade] %s x %s\n", push.Trades[0].Price, push.Trades[0].Size)
		}
	}, errHandler)
	if err != nil {
		return step("WatchTrades", err)
	}
	<-streamCtx.Done()
	fmt.Printf("streams: tickers=%d books=%d stats=%d tradePushes=%d connected=%v\n", tickers, books, stats, tradePushes, perps.Stream().IsConnected())

	// 4. Rate-limit accounting.
	var limits lighter.RateLimitSnapshot = client.RateLimits()
	fmt.Printf("rate limits: tier=%s weight=%d/%d requests=%d/%d\n", limits.Tier, limits.UsedWeight, limits.WeightLimit, limits.UsedRequests, limits.RequestLimit)
	return nil
}
