/*
FILE: types/enums.go

DESCRIPTION:
Protocol-level enums shared by every section (layer 1).

Two vocabularies coexist on Lighter:
  - transactions carry NUMERIC codes (order type 0..6, time in force 0..2,
    cancel-all time in force 0..2, margin mode 0..1, tx type 8..45) — the
    values signed into the transaction hash;
  - REST / WebSocket views carry STRING names ("limit", "good-till-time",
    "canceled-post-only", ...).
Each numeric enum below has a String() that renders the view name, so a value
read from the exchange can be compared with the constants directly.

Sources: lighter-go types/txtypes/constants.go, the "Signing Transactions"
and "Data Structures, Constants and Errors" pages, openapi.json of lighter-python.
*/

package types

// OrderType — order type code of a create-order transaction.
type OrderType uint8

const (
	// OrderTypeLimit — 0: limit order (any time in force).
	OrderTypeLimit OrderType = 0
	// OrderTypeMarket — 1: market order. Must be IOC with order expiry 0; the
	// price is the WORST acceptable execution price.
	OrderTypeMarket OrderType = 1
	// OrderTypeStopLoss — 2: stop-loss (market execution at trigger).
	OrderTypeStopLoss OrderType = 2
	// OrderTypeStopLossLimit — 3: stop-loss with a limit price.
	OrderTypeStopLossLimit OrderType = 3
	// OrderTypeTakeProfit — 4: take-profit (market execution at trigger).
	OrderTypeTakeProfit OrderType = 4
	// OrderTypeTakeProfitLimit — 5: take-profit with a limit price.
	OrderTypeTakeProfitLimit OrderType = 5
	// OrderTypeTWAP — 6: time-weighted average price order.
	OrderTypeTWAP OrderType = 6
	// OrderTypeTWAPSub — 7: internal, child of a TWAP (never sent).
	OrderTypeTWAPSub OrderType = 7
	// OrderTypeLiquidation — 8: internal liquidation order (never sent).
	OrderTypeLiquidation OrderType = 8
)

// String returns the view name of the order type.
func (t OrderType) String() string {
	switch t {
	case OrderTypeLimit:
		return "limit"
	case OrderTypeMarket:
		return "market"
	case OrderTypeStopLoss:
		return "stop-loss"
	case OrderTypeStopLossLimit:
		return "stop-loss-limit"
	case OrderTypeTakeProfit:
		return "take-profit"
	case OrderTypeTakeProfitLimit:
		return "take-profit-limit"
	case OrderTypeTWAP:
		return "twap"
	case OrderTypeTWAPSub:
		return "twap-sub"
	case OrderTypeLiquidation:
		return "liquidation"
	default:
		return "unknown"
	}
}

// Valid reports whether t is a type a client may send (0..6).
func (t OrderType) Valid() bool { return t <= OrderTypeTWAP }

// IsTrigger reports whether the type needs a trigger price (SL / TP kinds).
func (t OrderType) IsTrigger() bool {
	return t >= OrderTypeStopLoss && t <= OrderTypeTakeProfitLimit
}

// TimeInForce — time in force code of an order.
type TimeInForce uint8

const (
	// TimeInForceIOC — 0: immediate or cancel.
	TimeInForceIOC TimeInForce = 0
	// TimeInForceGTT — 1: good till time (order_expiry required, 5 min..30 days).
	TimeInForceGTT TimeInForce = 1
	// TimeInForcePostOnly — 2: post only (rejected if it would take).
	TimeInForcePostOnly TimeInForce = 2
)

// String returns the view name of the time in force.
func (t TimeInForce) String() string {
	switch t {
	case TimeInForceIOC:
		return "immediate-or-cancel"
	case TimeInForceGTT:
		return "good-till-time"
	case TimeInForcePostOnly:
		return "post-only"
	default:
		return "Unknown"
	}
}

// Valid reports whether t is one of the three exchange values.
func (t TimeInForce) Valid() bool { return t <= TimeInForcePostOnly }

// CancelAllTimeInForce — mode of a cancel-all-orders transaction.
type CancelAllTimeInForce uint8

const (
	// CancelAllImmediate — 0: cancel now (Time must be 0).
	CancelAllImmediate CancelAllTimeInForce = 0
	// CancelAllScheduled — 1: dead man's switch: cancel at Time (unix ms,
	// 5 min..15 days ahead).
	CancelAllScheduled CancelAllTimeInForce = 1
	// CancelAllAbort — 2: disarm a scheduled cancel-all (Time must be 0).
	CancelAllAbort CancelAllTimeInForce = 2
)

// Valid reports whether t is one of the three exchange values.
func (t CancelAllTimeInForce) Valid() bool { return t <= CancelAllAbort }

// MarginMode — margin mode of a position / leverage update.
type MarginMode uint8

const (
	// MarginModeCross — 0.
	MarginModeCross MarginMode = 0
	// MarginModeIsolated — 1.
	MarginModeIsolated MarginMode = 1
)

// Valid reports whether m is cross or isolated.
func (m MarginMode) Valid() bool { return m <= MarginModeIsolated }

// String returns the name of the margin mode.
func (m MarginMode) String() string {
	if m == MarginModeIsolated {
		return "isolated"
	}
	return "cross"
}

// GroupingType — grouping of a create-grouped-orders transaction.
type GroupingType uint8

const (
	// GroupingOneTriggersTheOther — 1: parent + one child (OTO).
	GroupingOneTriggersTheOther GroupingType = 1
	// GroupingOneCancelsTheOther — 2: two siblings, SL + TP (OCO).
	GroupingOneCancelsTheOther GroupingType = 2
	// GroupingOneTriggersOneCancelsTheOther — 3: parent + SL + TP (OTOCO).
	GroupingOneTriggersOneCancelsTheOther GroupingType = 3
)

// Valid reports whether g is one of the three exchange values.
func (g GroupingType) Valid() bool {
	return g >= GroupingOneTriggersTheOther && g <= GroupingOneTriggersOneCancelsTheOther
}

// TxType — transaction type code (lighter-go constants.go). Only the L2
// types a client can sign are listed.
type TxType uint8

const (
	// TxTypeL2ChangePubKey — 8.
	TxTypeL2ChangePubKey TxType = 8
	// TxTypeL2CreateSubAccount — 9.
	TxTypeL2CreateSubAccount TxType = 9
	// TxTypeL2CreatePublicPool — 10.
	TxTypeL2CreatePublicPool TxType = 10
	// TxTypeL2UpdatePublicPool — 11.
	TxTypeL2UpdatePublicPool TxType = 11
	// TxTypeL2Transfer — 12.
	TxTypeL2Transfer TxType = 12
	// TxTypeL2Withdraw — 13.
	TxTypeL2Withdraw TxType = 13
	// TxTypeL2CreateOrder — 14.
	TxTypeL2CreateOrder TxType = 14
	// TxTypeL2CancelOrder — 15.
	TxTypeL2CancelOrder TxType = 15
	// TxTypeL2CancelAllOrders — 16.
	TxTypeL2CancelAllOrders TxType = 16
	// TxTypeL2ModifyOrder — 17.
	TxTypeL2ModifyOrder TxType = 17
	// TxTypeL2MintShares — 18.
	TxTypeL2MintShares TxType = 18
	// TxTypeL2BurnShares — 19.
	TxTypeL2BurnShares TxType = 19
	// TxTypeL2UpdateLeverage — 20.
	TxTypeL2UpdateLeverage TxType = 20
	// TxTypeL2CreateGroupedOrders — 28.
	TxTypeL2CreateGroupedOrders TxType = 28
	// TxTypeL2UpdateMargin — 29.
	TxTypeL2UpdateMargin TxType = 29
	// TxTypeL2CreateStakingPool — 33.
	TxTypeL2CreateStakingPool TxType = 33
	// TxTypeL2StakeAssets — 35.
	TxTypeL2StakeAssets TxType = 35
	// TxTypeL2UnstakeAssets — 36.
	TxTypeL2UnstakeAssets TxType = 36
	// TxTypeL2UpdateAccountConfig — 41.
	TxTypeL2UpdateAccountConfig TxType = 41
	// TxTypeL2UpdateAccountAssetConfig — 42.
	TxTypeL2UpdateAccountAssetConfig TxType = 42
	// TxTypeL2ApproveIntegrator — 45.
	TxTypeL2ApproveIntegrator TxType = 45
)

// String returns a stable name of the transaction type (logs, metrics).
func (t TxType) String() string {
	switch t {
	case TxTypeL2ChangePubKey:
		return "changePubKey"
	case TxTypeL2CreateSubAccount:
		return "createSubAccount"
	case TxTypeL2CreatePublicPool:
		return "createPublicPool"
	case TxTypeL2UpdatePublicPool:
		return "updatePublicPool"
	case TxTypeL2Transfer:
		return "transfer"
	case TxTypeL2Withdraw:
		return "withdraw"
	case TxTypeL2CreateOrder:
		return "createOrder"
	case TxTypeL2CancelOrder:
		return "cancelOrder"
	case TxTypeL2CancelAllOrders:
		return "cancelAllOrders"
	case TxTypeL2ModifyOrder:
		return "modifyOrder"
	case TxTypeL2MintShares:
		return "mintShares"
	case TxTypeL2BurnShares:
		return "burnShares"
	case TxTypeL2UpdateLeverage:
		return "updateLeverage"
	case TxTypeL2CreateGroupedOrders:
		return "createGroupedOrders"
	case TxTypeL2UpdateMargin:
		return "updateMargin"
	case TxTypeL2CreateStakingPool:
		return "createStakingPool"
	case TxTypeL2StakeAssets:
		return "stakeAssets"
	case TxTypeL2UnstakeAssets:
		return "unstakeAssets"
	case TxTypeL2UpdateAccountConfig:
		return "updateAccountConfig"
	case TxTypeL2UpdateAccountAssetConfig:
		return "updateAccountAssetConfig"
	case TxTypeL2ApproveIntegrator:
		return "approveIntegrator"
	default:
		return "unknown"
	}
}

// OrderStatus — status string of an order view (REST and WebSocket).
type OrderStatus string

// Order statuses (openapi.json Order.status).
const (
	OrderStatusInProgress                OrderStatus = "in-progress"
	OrderStatusPending                   OrderStatus = "pending"
	OrderStatusOpen                      OrderStatus = "open"
	OrderStatusFilled                    OrderStatus = "filled"
	OrderStatusCanceled                  OrderStatus = "canceled"
	OrderStatusCanceledPostOnly          OrderStatus = "canceled-post-only"
	OrderStatusCanceledReduceOnly        OrderStatus = "canceled-reduce-only"
	OrderStatusCanceledPositionNotAllowd OrderStatus = "canceled-position-not-allowed"
	OrderStatusCanceledMarginNotAllowed  OrderStatus = "canceled-margin-not-allowed"
	OrderStatusCanceledTooMuchSlippage   OrderStatus = "canceled-too-much-slippage"
	OrderStatusCanceledNotEnoughLiquidty OrderStatus = "canceled-not-enough-liquidity"
	OrderStatusCanceledSelfTrade         OrderStatus = "canceled-self-trade"
	OrderStatusCanceledExpired           OrderStatus = "canceled-expired"
	OrderStatusCanceledOCO               OrderStatus = "canceled-oco"
	OrderStatusCanceledChild             OrderStatus = "canceled-child"
	OrderStatusCanceledLiquidation       OrderStatus = "canceled-liquidation"
	OrderStatusCanceledInvalidBalance    OrderStatus = "canceled-invalid-balance"
)

// IsCanceled reports whether the status is "canceled" or any "canceled-*" reason.
func (s OrderStatus) IsCanceled() bool {
	return len(s) >= 8 && s[:8] == "canceled"
}

// IsTerminal reports whether the order can no longer change (filled or canceled).
func (s OrderStatus) IsTerminal() bool {
	return s == OrderStatusFilled || s.IsCanceled()
}

// IsActive reports whether the order rests in the book or waits for a trigger.
func (s OrderStatus) IsActive() bool {
	return s == OrderStatusOpen || s == OrderStatusPending || s == OrderStatusInProgress
}

// TriggerStatus — trigger_status of an order view.
type TriggerStatus string

// Trigger statuses (openapi.json Order.trigger_status).
const (
	TriggerStatusNA          TriggerStatus = "na"
	TriggerStatusReady       TriggerStatus = "ready"
	TriggerStatusMarkPrice   TriggerStatus = "mark-price"
	TriggerStatusTWAP        TriggerStatus = "twap"
	TriggerStatusParentOrder TriggerStatus = "parent-order"
)

// TxStatus — status of a transaction as reported by the tx endpoints and the
// account_tx stream ("Transaction Status Mapping" of the constants page).
type TxStatus int64

const (
	// TxStatusFailed — 0: rejected by the sequencer.
	TxStatusFailed TxStatus = 0
	// TxStatusPending — 1: queued, not executed yet.
	TxStatusPending TxStatus = 1
	// TxStatusExecuted — 2: executed.
	TxStatusExecuted TxStatus = 2
	// TxStatusPendingFinal — 3: "Pending - Final State" (docs wording).
	TxStatusPendingFinal TxStatus = 3
)

// String returns a stable name of the status.
func (s TxStatus) String() string {
	switch s {
	case TxStatusFailed:
		return "failed"
	case TxStatusPending:
		return "pending"
	case TxStatusExecuted:
		return "executed"
	case TxStatusPendingFinal:
		return "pending-final"
	default:
		return "unknown"
	}
}

// MarketType — market_type of a market ("perp" / "spot").
type MarketType string

// Market types.
const (
	MarketTypePerp MarketType = "perp"
	MarketTypeSpot MarketType = "spot"
)

// MarketStatus — status of a market ("active" / "inactive").
type MarketStatus string

// Market statuses.
const (
	MarketStatusActive   MarketStatus = "active"
	MarketStatusInactive MarketStatus = "inactive"
)

// Candle resolutions accepted by the candles endpoint and the candle stream.
const (
	Resolution1m  string = "1m"
	Resolution5m  string = "5m"
	Resolution15m string = "15m"
	Resolution30m string = "30m"
	Resolution1h  string = "1h"
	Resolution4h  string = "4h"
	Resolution12h string = "12h"
	Resolution1d  string = "1d"
)

// ValidResolution reports whether r is one of the documented resolutions.
func ValidResolution(r string) bool {
	switch r {
	case Resolution1m, Resolution5m, Resolution15m, Resolution30m, Resolution1h, Resolution4h, Resolution12h, Resolution1d:
		return true
	default:
		return false
	}
}
