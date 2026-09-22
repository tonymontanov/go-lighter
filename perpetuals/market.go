/*
FILE: perpetuals/market.go

DESCRIPTION:
Market data of the Perpetuals section (REST). Every method is the unified
function of the common layer plus the section profile.

PRICES FOR TAKER ORDERS:
Lighter has a native market order, but its price field is the WORST
acceptable execution price. SlippagePrice derives it from a reference price
and a slippage in basis points, on the market's grid.
*/

package perpetuals

import (
	"context"

	"github.com/tonymontanov/go-lighter/internal/domain"
	"github.com/tonymontanov/go-lighter/internal/markets"
	"github.com/tonymontanov/go-lighter/types"
)

// MarketDataClient — market data sub-client.
type MarketDataClient struct {
	c *Client
}

// GetMarkets returns every perp market (loaded on first use, then cached and
// refreshed in the background).
func (m *MarketDataClient) GetMarkets(ctx context.Context) ([]types.MarketInfo, error) {
	var snapshot *markets.Snapshot
	var err error
	snapshot, err = m.c.registry.Get(ctx)
	if err != nil {
		return nil, err
	}
	return snapshot.All(), nil
}

// GetMarketInfo returns the static description of a market.
func (m *MarketDataClient) GetMarketInfo(ctx context.Context, symbol string) (types.MarketInfo, error) {
	var info *types.MarketInfo
	var err error
	info, err = m.c.prof().Resolve(ctx, "GetMarketInfo", symbol)
	if err != nil {
		return types.MarketInfo{}, err
	}
	return *info, nil
}

// GetMarketDetails fetches the live details of a market (mark / index price,
// funding clamps, daily statistics).
func (m *MarketDataClient) GetMarketDetails(ctx context.Context, symbol string) (types.MarketDetails, error) {
	return domain.MarketDetails(ctx, m.c.engine(), m.c.prof(), symbol)
}

// GetOrderBook fetches up to limit orders per side (1..250) aggregated into
// price levels, best first.
func (m *MarketDataClient) GetOrderBook(ctx context.Context, symbol string, limit int) (types.OrderBook, error) {
	return domain.OrderBookSnapshot(ctx, m.c.engine(), m.c.prof(), symbol, limit)
}

// GetRecentTrades fetches the latest public trades of a market.
func (m *MarketDataClient) GetRecentTrades(ctx context.Context, symbol string, limit int) ([]types.Trade, error) {
	return domain.RecentTrades(ctx, m.c.engine(), m.c.prof(), symbol, limit)
}

// GetCandles fetches candles between startMs and endMs (unix ms), at most
// countBack (<= 500) counted back from endMs.
func (m *MarketDataClient) GetCandles(ctx context.Context, symbol string, resolution string, startMs int64, endMs int64, countBack int) ([]types.Candle, error) {
	return domain.Candles(ctx, m.c.engine(), m.c.prof(), symbol, resolution, startMs, endMs, countBack)
}

// GetFundingRates fetches the funding rates of every market (Lighter's own
// rows have Exchange == "lighter").
func (m *MarketDataClient) GetFundingRates(ctx context.Context) ([]types.FundingRate, error) {
	return domain.FundingRates(ctx, m.c.engine())
}

/*
SlippagePrice returns the worst acceptable price of a taker order: reference
moved by slippageBps basis points against the order (up for buys, down for
sells) and snapped to the market grid in the same direction. reference is
typically the best opposite level of the book or the mark price.
*/
func (m *MarketDataClient) SlippagePrice(ctx context.Context, symbol string, reference types.Fixed, isAsk bool, slippageBps int64) (types.Fixed, error) {
	var info *types.MarketInfo
	var err error
	info, err = m.c.prof().Resolve(ctx, "SlippagePrice", symbol)
	if err != nil {
		return 0, err
	}
	return SlippagePrice(info.Precision, reference, isAsk, slippageBps), nil
}

// SlippagePrice — pure form of MarketDataClient.SlippagePrice.
func SlippagePrice(precision types.Precision, reference types.Fixed, isAsk bool, slippageBps int64) types.Fixed {
	if reference <= 0 {
		return reference
	}
	var delta types.Fixed = types.Fixed(int64(reference) * slippageBps / 10_000)
	if isAsk {
		return precision.NormalizePrice(reference-delta, types.RoundDown)
	}
	return precision.NormalizePrice(reference+delta, types.RoundUp)
}
