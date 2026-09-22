/*
FILE: types/market.go

DESCRIPTION:
Market-data domain types shared by every section (layer 1). They sit on the
read hot path (WS pushes), so prices and sizes are Fixed and the structs
decode straight from the exchange JSON (json tags carry the exchange names).
Values that can exceed the Fixed range (volumes, open interest, limits) are
decimal.Decimal.

Sources (openapi.json of lighter-python + the WebSocket reference):
  MarketInfo / MarketDetails : orderBooks, orderBookDetails
  OrderBookLevel, OrderBookUpdate : order_book channel (snapshot + deltas)
  OrderBook                  : local book maintained by the SDK
  Ticker                     : ticker channel (best bid / offer)
  Trade                      : recentTrades, trades, trade channel
  Candle                     : candles endpoint, candle channel
  MarketStats                : market_stats channel
  FundingRate                : funding-rates endpoint

ORDER BOOK MODEL:
The order_book channel sends a full snapshot on subscription, then state
changes batched every 50 ms. Continuity is verified with begin_nonce ==
previous nonce; the SDK resubscribes on a gap (see internal/orderbook).
Levels are aggregated by price; a size of 0 removes the level.
*/

package types

import "github.com/shopspring/decimal"

// MarketInfo — static description of one market, built from orderBookDetails.
type MarketInfo struct {
	// Symbol — exchange symbol ("ETH", "BTC"; spot markets use "ETH/USDC").
	Symbol string
	// MarketID — market index used in transactions and channels.
	MarketID int16
	// MarketType — perp or spot.
	MarketType MarketType
	// Status — active or inactive.
	Status MarketStatus
	// BaseAssetID / QuoteAssetID — asset ids of the pair.
	BaseAssetID  int16
	QuoteAssetID int16
	// TakerFee / MakerFee — fee rates as fractions ("0.0001" = 1 bp).
	TakerFee Fixed
	MakerFee Fixed
	// LiquidationFee — liquidation fee rate.
	LiquidationFee Fixed
	// MinBaseAmount / MinQuoteAmount — minimums of a MAKER order (the larger
	// applies); taker orders are exempt.
	MinBaseAmount  Fixed
	MinQuoteAmount Fixed
	// Precision — price / size grid (supported_price_decimals / supported_size_decimals).
	Precision Precision
	// QuoteDecimals — supported_quote_decimals.
	QuoteDecimals int
	// OrderQuoteLimit — maximum quote value of one order.
	OrderQuoteLimit Fixed
	// QuoteMultiplier — quote_multiplier of perp markets (1 for most).
	QuoteMultiplier int64
	// DefaultInitialMarginFraction / MinInitialMarginFraction /
	// MaintenanceMarginFraction / CloseoutMarginFraction — in 1/10000
	// (500 = 5% = 20x); perp markets only.
	DefaultInitialMarginFraction uint16
	MinInitialMarginFraction     uint16
	MaintenanceMarginFraction    uint16
	CloseoutMarginFraction       uint16
	// IsMakerFeeEnabled / IsTakerFeeEnabled — fee switches.
	IsMakerFeeEnabled bool
	IsTakerFeeEnabled bool
}

// MaxLeverage returns the maximum leverage allowed by MinInitialMarginFraction
// (10000 / fraction), or 0 when unknown.
func (m *MarketInfo) MaxLeverage() int {
	if m.MinInitialMarginFraction == 0 {
		return 0
	}
	return 10_000 / int(m.MinInitialMarginFraction)
}

// MarketDetails — MarketInfo plus the live statistics of orderBookDetails.
type MarketDetails struct {
	MarketInfo
	LastTradePrice        Fixed
	DailyTradesCount      int64
	DailyBaseTokenVolume  decimal.Decimal
	DailyQuoteTokenVolume decimal.Decimal
	DailyPriceLow         Fixed
	DailyPriceHigh        Fixed
	DailyPriceChange      float64
	OpenInterest          decimal.Decimal
	MarkPrice             Fixed
	IndexPrice            Fixed
	FundingClampSmall     Fixed
	FundingClampBig       Fixed
	BaseInterestRate      Fixed
}

// OrderBookLevel — one aggregated price level.
type OrderBookLevel struct {
	Price Fixed `json:"price"`
	Size  Fixed `json:"size"`
}

// OrderBookUpdate — one push of the order_book channel: the snapshot on
// subscription (IsSnapshot) or a batch of changed levels (size 0 = removed).
type OrderBookUpdate struct {
	// MarketID — market of the push.
	MarketID int16
	// IsSnapshot — true for the "subscribed/order_book" frame.
	IsSnapshot bool
	Asks       []OrderBookLevel
	Bids       []OrderBookLevel
	// Offset — API-server offset (changes on reconnection, not continuous).
	Offset int64
	// Nonce — matching-engine nonce after this update (last_nonce).
	Nonce int64
	// BeginNonce — nonce the update starts from; equals the previous Nonce
	// when no update was missed.
	BeginNonce int64
	// LastUpdatedAtUs — exchange timestamp in microseconds.
	LastUpdatedAtUs int64
	// TimestampMs — frame timestamp in milliseconds.
	TimestampMs int64
}

// OrderBook — local order book maintained by the SDK. Asks ascending, bids
// descending, best first. The slices are owned by the SDK and reused between
// callbacks: copy what must outlive the callback.
type OrderBook struct {
	MarketID int16
	Asks     []OrderBookLevel
	Bids     []OrderBookLevel
	// Nonce — nonce of the last applied update.
	Nonce int64
	// LastUpdatedAtUs — exchange timestamp of the last applied update.
	LastUpdatedAtUs int64
	// ReceivedAtNs — local monotonic-ish receive time of the last update.
	ReceivedAtNs int64
}

// BestBid returns the best bid or nil when the side is empty.
func (b *OrderBook) BestBid() *OrderBookLevel {
	if len(b.Bids) == 0 {
		return nil
	}
	return &b.Bids[0]
}

// BestAsk returns the best ask or nil when the side is empty.
func (b *OrderBook) BestAsk() *OrderBookLevel {
	if len(b.Asks) == 0 {
		return nil
	}
	return &b.Asks[0]
}

// Ticker — best bid / offer push of the ticker channel.
type Ticker struct {
	MarketID int16
	// Symbol — "s" field.
	Symbol string
	// Ask / Bid — best levels; a zero level means the side is empty.
	Ask OrderBookLevel
	Bid OrderBookLevel
	// Nonce — matching-engine nonce.
	Nonce int64
	// LastUpdatedAtUs — exchange timestamp in microseconds.
	LastUpdatedAtUs int64
	// TimestampMs — frame timestamp in milliseconds.
	TimestampMs int64
}

// Trade — one trade (public or of the account).
type Trade struct {
	TradeID int64  `json:"trade_id"`
	TxHash  string `json:"tx_hash"`
	// Type — "trade", "liquidation", "deleverage", "market-settlement".
	Type      string `json:"type"`
	MarketID  int16  `json:"market_id"`
	Size      Fixed  `json:"size"`
	Price     Fixed  `json:"price"`
	USDAmount Fixed  `json:"usd_amount"`
	// AskID / BidID — order indexes of the two sides.
	AskID int64 `json:"ask_id"`
	BidID int64 `json:"bid_id"`
	// AskClientID / BidClientID — client order indexes (0 when none).
	AskClientID  int64 `json:"ask_client_id"`
	BidClientID  int64 `json:"bid_client_id"`
	AskAccountID int64 `json:"ask_account_id"`
	BidAccountID int64 `json:"bid_account_id"`
	// IsMakerAsk — true when the ask side was the maker.
	IsMakerAsk  bool  `json:"is_maker_ask"`
	BlockHeight int64 `json:"block_height"`
	// TimestampMs — trade time in milliseconds.
	TimestampMs int64 `json:"timestamp"`
	// TakerFee / MakerFee — raw integer fees as sent by the exchange (omitted
	// when zero); the unit is not documented.
	TakerFee int64 `json:"taker_fee"`
	MakerFee int64 `json:"maker_fee"`
	// Position sizes and entry quotes of both sides BEFORE the trade.
	TakerPositionSizeBefore Fixed           `json:"taker_position_size_before"`
	TakerEntryQuoteBefore   decimal.Decimal `json:"taker_entry_quote_before"`
	MakerPositionSizeBefore Fixed           `json:"maker_position_size_before"`
	MakerEntryQuoteBefore   decimal.Decimal `json:"maker_entry_quote_before"`
	// TransactionTimeUs — sequencer time in microseconds.
	TransactionTimeUs int64 `json:"transaction_time"`
	// AskOrderVersion / BidOrderVersion — order versions (modify tracking).
	AskOrderVersion int64 `json:"ask_order_version"`
	BidOrderVersion int64 `json:"bid_order_version"`
	// BidAccountPnl / AskAccountPnl — realised PnL of the queried account
	// (trades endpoint with account_index only).
	BidAccountPnl decimal.Decimal `json:"bid_account_pnl"`
	AskAccountPnl decimal.Decimal `json:"ask_account_pnl"`
}

// Candle — one OHLCV candle (candles endpoint and candle channel).
type Candle struct {
	// OpenTimeMs — candle open time in milliseconds.
	OpenTimeMs int64 `json:"t"`
	Open       Fixed `json:"o"`
	High       Fixed `json:"h"`
	Low        Fixed `json:"l"`
	Close      Fixed `json:"c"`
	// BaseVolume — "v"; QuoteVolume — "V".
	BaseVolume  decimal.Decimal `json:"v"`
	QuoteVolume decimal.Decimal `json:"V"`
	// LastTradeID — "i".
	LastTradeID int64 `json:"i"`
}

// MarketStats — market_stats channel push (perp markets).
type MarketStats struct {
	Symbol   string `json:"symbol"`
	MarketID int16  `json:"market_id"`
	// IndexPrice / MarkPrice / MidPrice / BestAskPrice / BestBidPrice / LastTradePrice.
	IndexPrice     Fixed `json:"index_price"`
	MarkPrice      Fixed `json:"mark_price"`
	MidPrice       Fixed `json:"mid_price"`
	BestAskPrice   Fixed `json:"best_ask_price"`
	BestBidPrice   Fixed `json:"best_bid_price"`
	LastTradePrice Fixed `json:"last_trade_price"`
	// OpenInterest / OpenInterestLimit — decimal: the limit can be 2^56.
	OpenInterest      decimal.Decimal `json:"open_interest"`
	OpenInterestLimit decimal.Decimal `json:"open_interest_limit"`
	FundingClampSmall Fixed           `json:"funding_clamp_small"`
	FundingClampBig   Fixed           `json:"funding_clamp_big"`
	// CurrentFundingRate — estimate of the upcoming payment; FundingRate —
	// the last payment (at FundingTimestampMs). Percent per period.
	CurrentFundingRate Fixed `json:"current_funding_rate"`
	FundingRate        Fixed `json:"funding_rate"`
	FundingTimestampMs int64 `json:"funding_timestamp"`
	// Daily statistics (floats on the wire).
	DailyBaseTokenVolume  decimal.Decimal `json:"daily_base_token_volume"`
	DailyQuoteTokenVolume decimal.Decimal `json:"daily_quote_token_volume"`
	DailyPriceLow         Fixed           `json:"daily_price_low"`
	DailyPriceHigh        Fixed           `json:"daily_price_high"`
	DailyPriceChange      float64         `json:"daily_price_change"`
	BaseInterestRate      Fixed           `json:"base_interest_rate"`
	// Premium — percentage.
	Premium Fixed `json:"premium"`
}

// FundingRate — one entry of the funding-rates endpoint (the exchange also
// lists Binance / Bybit / Hyperliquid rates of the same symbol).
type FundingRate struct {
	MarketID int16   `json:"market_id"`
	Exchange string  `json:"exchange"`
	Symbol   string  `json:"symbol"`
	Rate     float64 `json:"rate"`
}
