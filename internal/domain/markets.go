/*
FILE: internal/domain/markets.go

DESCRIPTION:
Unified market-metadata and market-data reads of the common layer.

ENDPOINTS (openapi.json, verified live on 2026-09-22):
  GET orderBookDetails?filter=<perp|spot>[&market_id=N]
      → order_book_details[] / spot_order_book_details[] (PerpsOrderBookDetail)
  GET orderBookOrders?market_id=N&limit=L (1..250)
      → asks[] / bids[] of individual orders (SimpleOrder) — aggregated by
        price into levels here
  GET recentTrades?market_id=N&limit=L   → trades[]
  GET candles?market_id=N&resolution=R&start_timestamp=S&end_timestamp=E&count_back=C
      → c[] (Candle), max 500 per call
  GET funding-rates                       → funding_rates[]

The live orderBookDetails answer carries more keys than the OpenAPI
document (is_frozen, settlement_*, operator_account_index, outcome, ...);
only documented keys are modelled and decoding is lenient.
*/

package domain

import (
	"context"

	"github.com/shopspring/decimal"

	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/internal/ratelimit"
	"github.com/tonymontanov/go-lighter/internal/rest"
	"github.com/tonymontanov/go-lighter/types"
)

// marketDetailWire — one entry of orderBookDetails (documented keys only).
type marketDetailWire struct {
	Symbol                       string          `json:"symbol"`
	MarketID                     int16           `json:"market_id"`
	MarketType                   string          `json:"market_type"`
	BaseAssetID                  int16           `json:"base_asset_id"`
	QuoteAssetID                 int16           `json:"quote_asset_id"`
	Status                       string          `json:"status"`
	TakerFee                     types.Fixed     `json:"taker_fee"`
	MakerFee                     types.Fixed     `json:"maker_fee"`
	LiquidationFee               types.Fixed     `json:"liquidation_fee"`
	MinBaseAmount                types.Fixed     `json:"min_base_amount"`
	MinQuoteAmount               types.Fixed     `json:"min_quote_amount"`
	SupportedSizeDecimals        int             `json:"supported_size_decimals"`
	SupportedPriceDecimals       int             `json:"supported_price_decimals"`
	SupportedQuoteDecimals       int             `json:"supported_quote_decimals"`
	OrderQuoteLimit              types.Fixed     `json:"order_quote_limit"`
	QuoteMultiplier              int64           `json:"quote_multiplier"`
	DefaultInitialMarginFraction uint16          `json:"default_initial_margin_fraction"`
	MinInitialMarginFraction     uint16          `json:"min_initial_margin_fraction"`
	MaintenanceMarginFraction    uint16          `json:"maintenance_margin_fraction"`
	CloseoutMarginFraction       uint16          `json:"closeout_margin_fraction"`
	IsMakerFeeEnabled            bool            `json:"is_maker_fee_enabled"`
	IsTakerFeeEnabled            bool            `json:"is_taker_fee_enabled"`
	LastTradePrice               types.Fixed     `json:"last_trade_price"`
	DailyTradesCount             int64           `json:"daily_trades_count"`
	DailyBaseTokenVolume         decimal.Decimal `json:"daily_base_token_volume"`
	DailyQuoteTokenVolume        decimal.Decimal `json:"daily_quote_token_volume"`
	DailyPriceLow                types.Fixed     `json:"daily_price_low"`
	DailyPriceHigh               types.Fixed     `json:"daily_price_high"`
	DailyPriceChange             float64         `json:"daily_price_change"`
	OpenInterest                 decimal.Decimal `json:"open_interest"`
	MarkPrice                    types.Fixed     `json:"mark_price"`
	IndexPrice                   types.Fixed     `json:"index_price"`
	FundingClampSmall            types.Fixed     `json:"funding_clamp_small"`
	FundingClampBig              types.Fixed     `json:"funding_clamp_big"`
	BaseInterestRate             types.Fixed     `json:"base_interest_rate"`
}

// marketDetailsResponse — orderBookDetails envelope.
type marketDetailsResponse struct {
	OrderBookDetails     []marketDetailWire `json:"order_book_details"`
	SpotOrderBookDetails []marketDetailWire `json:"spot_order_book_details"`
}

// toInfo converts a wire entry to the public MarketInfo.
func (w *marketDetailWire) toInfo() types.MarketInfo {
	return types.MarketInfo{
		Symbol:                       w.Symbol,
		MarketID:                     w.MarketID,
		MarketType:                   types.MarketType(w.MarketType),
		Status:                       types.MarketStatus(w.Status),
		BaseAssetID:                  w.BaseAssetID,
		QuoteAssetID:                 w.QuoteAssetID,
		TakerFee:                     w.TakerFee,
		MakerFee:                     w.MakerFee,
		LiquidationFee:               w.LiquidationFee,
		MinBaseAmount:                w.MinBaseAmount,
		MinQuoteAmount:               w.MinQuoteAmount,
		Precision:                    types.Precision{PriceDecimals: w.SupportedPriceDecimals, SizeDecimals: w.SupportedSizeDecimals},
		QuoteDecimals:                w.SupportedQuoteDecimals,
		OrderQuoteLimit:              w.OrderQuoteLimit,
		QuoteMultiplier:              w.QuoteMultiplier,
		DefaultInitialMarginFraction: w.DefaultInitialMarginFraction,
		MinInitialMarginFraction:     w.MinInitialMarginFraction,
		MaintenanceMarginFraction:    w.MaintenanceMarginFraction,
		CloseoutMarginFraction:       w.CloseoutMarginFraction,
		IsMakerFeeEnabled:            w.IsMakerFeeEnabled,
		IsTakerFeeEnabled:            w.IsTakerFeeEnabled,
	}
}

// toDetails converts a wire entry to the public MarketDetails.
func (w *marketDetailWire) toDetails() types.MarketDetails {
	return types.MarketDetails{
		MarketInfo:            w.toInfo(),
		LastTradePrice:        w.LastTradePrice,
		DailyTradesCount:      w.DailyTradesCount,
		DailyBaseTokenVolume:  w.DailyBaseTokenVolume,
		DailyQuoteTokenVolume: w.DailyQuoteTokenVolume,
		DailyPriceLow:         w.DailyPriceLow,
		DailyPriceHigh:        w.DailyPriceHigh,
		DailyPriceChange:      w.DailyPriceChange,
		OpenInterest:          w.OpenInterest,
		MarkPrice:             w.MarkPrice,
		IndexPrice:            w.IndexPrice,
		FundingClampSmall:     w.FundingClampSmall,
		FundingClampBig:       w.FundingClampBig,
		BaseInterestRate:      w.BaseInterestRate,
	}
}

// entriesOf returns the list matching the section's market type.
func (r *marketDetailsResponse) entriesOf(marketType types.MarketType) []marketDetailWire {
	if marketType == types.MarketTypeSpot {
		return r.SpotOrderBookDetails
	}
	return r.OrderBookDetails
}

/*
LoadMarkets fetches orderBookDetails filtered by the profile's market type
and converts it into MarketInfo values — the registry loader of a section.
*/
func LoadMarkets(ctx context.Context, e *engine.Engine, p *Profile) ([]types.MarketInfo, error) {
	var q rest.Query
	q.Str("filter", string(p.MarketType))
	var out marketDetailsResponse
	var err error = e.Query(ctx, "orderBookDetails", q.String(), false, &out, ratelimit.CategoryMarket)
	if err != nil {
		return nil, err
	}
	var entries []marketDetailWire = out.entriesOf(p.MarketType)
	var list []types.MarketInfo = make([]types.MarketInfo, 0, len(entries))
	var i int
	for i = 0; i < len(entries); i++ {
		var info types.MarketInfo = entries[i].toInfo()
		if !info.Precision.Valid() {
			// A market with more than 8 decimals cannot be represented by
			// Fixed; it is skipped rather than mis-scaled.
			continue
		}
		list = append(list, info)
	}
	return list, nil
}

// MarketDetails fetches the live details (mark / index price, statistics) of
// one market.
func MarketDetails(ctx context.Context, e *engine.Engine, p *Profile, symbol string) (types.MarketDetails, error) {
	const operation string = "MarketDetails"
	var out types.MarketDetails
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return out, err
	}
	var q rest.Query
	q.Int("market_id", int64(info.MarketID)).Str("filter", string(p.MarketType))
	var resp marketDetailsResponse
	err = e.Query(ctx, "orderBookDetails", q.String(), false, &resp, ratelimit.CategoryMarket)
	if err != nil {
		return out, err
	}
	var entries []marketDetailWire = resp.entriesOf(p.MarketType)
	var i int
	for i = 0; i < len(entries); i++ {
		if entries[i].MarketID == info.MarketID {
			return entries[i].toDetails(), nil
		}
	}
	return out, p.invalid(operation, "market "+symbol+" is not in the orderBookDetails answer")
}

// simpleOrderWire — one order of orderBookOrders.
type simpleOrderWire struct {
	Price               types.Fixed `json:"price"`
	RemainingBaseAmount types.Fixed `json:"remaining_base_amount"`
}

// orderBookOrdersResponse — orderBookOrders envelope.
type orderBookOrdersResponse struct {
	TotalAsks int64             `json:"total_asks"`
	Asks      []simpleOrderWire `json:"asks"`
	TotalBids int64             `json:"total_bids"`
	Bids      []simpleOrderWire `json:"bids"`
}

// aggregateLevels sums the remaining amounts of consecutive orders at the
// same price. The exchange returns orders sorted by price from the best.
func aggregateLevels(orders []simpleOrderWire) []types.OrderBookLevel {
	var levels []types.OrderBookLevel = make([]types.OrderBookLevel, 0, len(orders))
	var i int
	for i = 0; i < len(orders); i++ {
		if orders[i].RemainingBaseAmount <= 0 {
			continue
		}
		var n int = len(levels)
		if n > 0 && levels[n-1].Price == orders[i].Price {
			levels[n-1].Size += orders[i].RemainingBaseAmount
			continue
		}
		levels = append(levels, types.OrderBookLevel{Price: orders[i].Price, Size: orders[i].RemainingBaseAmount})
	}
	return levels
}

/*
OrderBookSnapshot fetches up to limit ORDERS per side (1..250) and aggregates
them into price levels. Note: the limit counts orders, not levels — the
returned depth in levels can be smaller.
*/
func OrderBookSnapshot(ctx context.Context, e *engine.Engine, p *Profile, symbol string, limit int) (types.OrderBook, error) {
	const operation string = "OrderBookSnapshot"
	var out types.OrderBook
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return out, err
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 250 {
		limit = 250
	}
	var q rest.Query
	q.Int("market_id", int64(info.MarketID)).Int("limit", int64(limit))
	var resp orderBookOrdersResponse
	err = e.Query(ctx, "orderBookOrders", q.String(), false, &resp, ratelimit.CategoryMarket)
	if err != nil {
		return out, err
	}
	out.MarketID = info.MarketID
	out.Asks = aggregateLevels(resp.Asks)
	out.Bids = aggregateLevels(resp.Bids)
	return out, nil
}

// tradesResponse — recentTrades / trades envelope.
type tradesResponse struct {
	Trades []types.Trade `json:"trades"`
}

// RecentTrades fetches the latest trades of a market (limit 1..).
func RecentTrades(ctx context.Context, e *engine.Engine, p *Profile, symbol string, limit int) ([]types.Trade, error) {
	const operation string = "RecentTrades"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	var q rest.Query
	q.Int("market_id", int64(info.MarketID)).Int("limit", int64(limit))
	var resp tradesResponse
	err = e.Query(ctx, "recentTrades", q.String(), false, &resp, ratelimit.CategoryMarket)
	if err != nil {
		return nil, err
	}
	return resp.Trades, nil
}

// candlesResponse — candles envelope.
type candlesResponse struct {
	Resolution string         `json:"r"`
	Candles    []types.Candle `json:"c"`
}

/*
Candles fetches candles of a market. startMs / endMs bound the range (unix
ms); countBack limits the number of candles counted back from endMs (at most
500 per call, an exchange limit).
*/
func Candles(ctx context.Context, e *engine.Engine, p *Profile, symbol string, resolution string, startMs int64, endMs int64, countBack int) ([]types.Candle, error) {
	const operation string = "Candles"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return nil, err
	}
	if !types.ValidResolution(resolution) {
		return nil, p.invalid(operation, "invalid resolution "+resolution)
	}
	if countBack <= 0 {
		countBack = 500
	}
	var q rest.Query
	q.Int("market_id", int64(info.MarketID)).Str("resolution", resolution).Int("start_timestamp", startMs).Int("end_timestamp", endMs).Int("count_back", int64(countBack))
	var resp candlesResponse
	err = e.Query(ctx, "candles", q.String(), false, &resp, ratelimit.CategoryMarket)
	if err != nil {
		return nil, err
	}
	return resp.Candles, nil
}

// fundingRatesResponse — funding-rates envelope.
type fundingRatesResponse struct {
	FundingRates []types.FundingRate `json:"funding_rates"`
}

// FundingRates fetches the funding rates of every market (Lighter's own rows
// have Exchange == "lighter"; the answer also lists other venues).
func FundingRates(ctx context.Context, e *engine.Engine) ([]types.FundingRate, error) {
	var resp fundingRatesResponse
	var err error = e.Query(ctx, "funding-rates", "", false, &resp, ratelimit.CategoryMarket)
	if err != nil {
		return nil, err
	}
	return resp.FundingRates, nil
}
