/*
FILE: types/account.go

DESCRIPTION:
Account-side domain types shared by every section (layer 1). Positions arrive
over WebSocket too, so their prices and sizes are Fixed; money-like account
figures (collateral, PnL, values) are decimal.Decimal.

Structs decode straight from the exchange JSON; json tags carry the exchange
field names. Sources: account (DetailedAccount), account_all_positions /
account_all / user_stats channels, apikeys, accountLimits (openapi.json +
the WebSocket reference).
*/

package types

import "github.com/shopspring/decimal"

// Position — one position of the account (REST account and the position channels).
type Position struct {
	MarketID int16  `json:"market_id"`
	Symbol   string `json:"symbol"`
	// InitialMarginFraction — as sent by the exchange (string).
	InitialMarginFraction Fixed `json:"initial_margin_fraction"`
	OpenOrderCount        int64 `json:"open_order_count"`
	PendingOrderCount     int64 `json:"pending_order_count"`
	PositionTiedOrders    int64 `json:"position_tied_order_count"`
	// Sign — 1 long, -1 short, 0 flat.
	Sign int32 `json:"sign"`
	// Position — unsigned size; see SignedSize.
	Position      Fixed           `json:"position"`
	AvgEntryPrice Fixed           `json:"avg_entry_price"`
	PositionValue decimal.Decimal `json:"position_value"`
	UnrealizedPnl decimal.Decimal `json:"unrealized_pnl"`
	RealizedPnl   decimal.Decimal `json:"realized_pnl"`
	// LiquidationPrice — 0 when not applicable.
	LiquidationPrice    Fixed           `json:"liquidation_price"`
	TotalFundingPaidOut decimal.Decimal `json:"total_funding_paid_out"`
	// MarginMode — 0 cross, 1 isolated.
	MarginMode      int32           `json:"margin_mode"`
	AllocatedMargin decimal.Decimal `json:"allocated_margin"`
	TotalDiscount   decimal.Decimal `json:"total_discount"`
	MarginSetFlag   int32           `json:"margin_set_flag"`
}

// SignedSize returns the position size with the sign applied (long > 0).
func (p *Position) SignedSize() Fixed {
	if p.Sign < 0 {
		return -p.Position
	}
	return p.Position
}

// IsFlat reports whether the position is zero.
func (p *Position) IsFlat() bool { return p.Position == 0 || p.Sign == 0 }

// AccountAsset — one spot / collateral asset balance of the account.
type AccountAsset struct {
	Symbol        string          `json:"symbol"`
	AssetID       int16           `json:"asset_id"`
	Balance       decimal.Decimal `json:"balance"`
	LockedBalance decimal.Decimal `json:"locked_balance"`
	MarginBalance decimal.Decimal `json:"margin_balance"`
	// MarginMode — "enabled" / "disabled".
	MarginMode string          `json:"margin_mode"`
	Multiplier decimal.Decimal `json:"multiplier"`
}

// Account — detailed account view (account endpoint, DetailedAccount).
type Account struct {
	// AccountType — exchange account type code.
	AccountType uint8 `json:"account_type"`
	// AccountTradingMode — 0 classic, 1 unified.
	AccountTradingMode uint8  `json:"account_trading_mode"`
	Index              int64  `json:"index"`
	L1Address          string `json:"l1_address"`
	// CancelAllTimeMs — armed dead man's switch (0 when none).
	CancelAllTimeMs         int64           `json:"cancel_all_time"`
	TotalOrderCount         int64           `json:"total_order_count"`
	TotalIsolatedOrderCount int64           `json:"total_isolated_order_count"`
	PendingOrderCount       int64           `json:"pending_order_count"`
	AvailableBalance        decimal.Decimal `json:"available_balance"`
	Status                  uint8           `json:"status"`
	Collateral              decimal.Decimal `json:"collateral"`
	Name                    string          `json:"name"`
	Description             string          `json:"description"`
	Positions               []Position      `json:"positions"`
	Assets                  []AccountAsset  `json:"assets"`
	TotalAssetValue         decimal.Decimal `json:"total_asset_value"`
	CrossAssetValue         decimal.Decimal `json:"cross_asset_value"`
	CrossInitialMarginReq   decimal.Decimal `json:"cross_initial_margin_requirement"`
	CrossMaintenanceMargin  decimal.Decimal `json:"cross_maintenance_margin_requirement"`
	CreatedAtMs             int64           `json:"created_at"`
	TransactionTimeUs       int64           `json:"transaction_time"`
}

// PositionOf returns the position of a market id, or nil.
func (a *Account) PositionOf(marketID int16) *Position {
	var i int
	for i = 0; i < len(a.Positions); i++ {
		if a.Positions[i].MarketID == marketID {
			return &a.Positions[i]
		}
	}
	return nil
}

// Balance — collateral summary derived from Account.
type Balance struct {
	// Collateral — deposited collateral (USDC).
	Collateral decimal.Decimal
	// AvailableBalance — free collateral.
	AvailableBalance decimal.Decimal
	// TotalAssetValue / CrossAssetValue — account values reported by the exchange.
	TotalAssetValue decimal.Decimal
	CrossAssetValue decimal.Decimal
}

// MarginStats — one block of the user_stats channel.
type MarginStats struct {
	Collateral       decimal.Decimal `json:"collateral"`
	PortfolioValue   decimal.Decimal `json:"portfolio_value"`
	Leverage         decimal.Decimal `json:"leverage"`
	AvailableBalance decimal.Decimal `json:"available_balance"`
	MarginUsage      decimal.Decimal `json:"margin_usage"`
	BuyingPower      decimal.Decimal `json:"buying_power"`
}

// AccountStats — user_stats channel push.
type AccountStats struct {
	MarginStats
	AccountTradingMode int         `json:"account_trading_mode"`
	CrossStats         MarginStats `json:"cross_stats"`
	TotalStats         MarginStats `json:"total_stats"`
	// TimestampMs — frame timestamp.
	TimestampMs int64 `json:"-"`
}

// APIKey — one registered API key of the account (apikeys endpoint).
type APIKey struct {
	AccountIndex int64 `json:"account_index"`
	APIKeyIndex  uint8 `json:"api_key_index"`
	// Nonce — next nonce of the key at the time of the query.
	Nonce int64 `json:"nonce"`
	// PublicKey — 80 hex characters, no prefix.
	PublicKey         string `json:"public_key"`
	TransactionTimeUs int64  `json:"transaction_time"`
}

// AccountLimits — accountLimits endpoint (tier and fee ticks).
type AccountLimits struct {
	MaxLLPPercentage     int32           `json:"max_llp_percentage"`
	UserTier             string          `json:"user_tier"`
	UserTierName         string          `json:"user_tier_name"`
	CanCreatePublicPool  bool            `json:"can_create_public_pool"`
	MaxLLPAmount         decimal.Decimal `json:"max_llp_amount"`
	CurrentMakerFeeTick  int32           `json:"current_maker_fee_tick"`
	CurrentTakerFeeTick  int32           `json:"current_taker_fee_tick"`
	EffectiveLITStakes   decimal.Decimal `json:"effective_lit_stakes"`
	LeasedLIT            decimal.Decimal `json:"leased_lit"`
	UserTierLastUpdateMs int64           `json:"user_tier_last_update"`
}

// PositionFunding — one funding payment of a position.
type PositionFunding struct {
	TimestampMs  int64  `json:"timestamp"`
	MarketID     int16  `json:"market_id"`
	FundingID    int64  `json:"funding_id"`
	Change       Fixed  `json:"change"`
	Discount     Fixed  `json:"discount"`
	Rate         Fixed  `json:"rate"`
	PositionSize Fixed  `json:"position_size"`
	PositionSide string `json:"position_side"`
}
