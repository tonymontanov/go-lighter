/*
FILE: types/orders.go

DESCRIPTION:
Order-related domain types shared by every section (layer 1): requests
accepted by Trading() sub-clients and the order view returned by the exchange.

REQUESTS use exchange vocabulary (symbol, isAsk, client order index) and
Fixed numerics — they sit on the order hot path and must not allocate. The
section resolves the symbol to a market index and scales prices / sizes with
the market's Precision; the request itself carries human units.

VIEW (Order) mirrors the exchange JSON one-to-one (json tags are the
exchange field names). Sources: accountActiveOrders / accountInactiveOrders /
accountOrders and the account_all_orders / account_orders channels.

ORDER EXPIRY:
GTT / post-only / trigger / TWAP orders need order_expiry, a unix ms
timestamp 5 minutes .. 30 days ahead. OrderExpiryMs == 0 means "use the
client default" (Config.DefaultOrderExpiry, 28 days like the official SDK).
IOC and market orders must not carry an expiry.
*/

package types

// CreateOrderRequest — one order to place.
type CreateOrderRequest struct {
	// Symbol — exchange symbol ("ETH").
	Symbol string
	// ClientOrderIndex — optional client reference, 1..2^48-1; 0 = none.
	// Cancels and modifies may reference the order by it.
	ClientOrderIndex int64
	// IsAsk — true sells, false buys.
	IsAsk bool
	// Price — limit price; for a market order the WORST acceptable price;
	// for SL / TP the execution price bounding the slippage.
	Price Fixed
	// Size — base amount. 0 is allowed only with ReduceOnly (close the
	// whole position).
	Size Fixed
	// Type — order type (limit by default).
	Type OrderType
	// TimeInForce — IOC / GTT / post-only.
	TimeInForce TimeInForce
	// ReduceOnly — the order may only reduce the position.
	ReduceOnly bool
	// TriggerPrice — trigger of SL / TP kinds; 0 otherwise.
	TriggerPrice Fixed
	// OrderExpiryMs — see the file header; 0 = client default.
	OrderExpiryMs int64
}

// ModifyOrderRequest — replace price / size / trigger of a resting order.
// Side and type cannot change.
type ModifyOrderRequest struct {
	Symbol string
	// OrderIndex — client order index or exchange order index of the order.
	OrderIndex int64
	// Price / Size / TriggerPrice — the new values (all three are sent).
	Price        Fixed
	Size         Fixed
	TriggerPrice Fixed
	// OrderVersion — optional: the sequencer rejects the modification unless
	// it is strictly greater than the stored version; 0 = not used.
	OrderVersion int64
}

// CancelOrderRequest — cancel one order.
type CancelOrderRequest struct {
	Symbol string
	// OrderIndex — client order index or exchange order index.
	OrderIndex int64
}

// CancelAllOrdersRequest — cancel every order of the account (or of one market).
type CancelAllOrdersRequest struct {
	// Symbol — limit the cancel to one market; "" = every market. Only valid
	// with CancelAllImmediate.
	Symbol string
	// TimeInForce — immediate / scheduled (dead man's switch) / abort.
	TimeInForce CancelAllTimeInForce
	// TimeMs — unix ms of a scheduled cancel (5 min .. 15 days ahead); 0 otherwise.
	TimeMs int64
}

// SendOptions — per-call options of any transaction-sending method.
type SendOptions struct {
	// APIKeyIndex — sign with this key of the pool; 0 = the client default
	// (its first key).
	APIKeyIndex uint8
	// Nonce — explicit nonce; 0 = taken from the key's lane. When set, the
	// lane is bypassed entirely: the caller owns nonce management.
	Nonce int64
	// ExpiredAtMs — transaction expiry (unix ms); 0 = now + Config.TxExpiry.
	ExpiredAtMs int64
	// SkipNonce — mark the transaction with the SkipNonce attribute
	// regardless of the client's nonce mode (with an explicit Nonce).
	SkipNonce bool
	// DisablePriceProtection — send price_protection=false (REST only). The
	// exchange documents the flag as "defaults to true" without describing
	// its effect; leave it alone unless the exchange tells you otherwise.
	DisablePriceProtection bool
}

// Order — order view of the exchange.
type Order struct {
	OrderIndex        int64  `json:"order_index"`
	ClientOrderIndex  int64  `json:"client_order_index"`
	OrderID           string `json:"order_id"`
	ClientOrderID     string `json:"client_order_id"`
	MarketIndex       int16  `json:"market_index"`
	OwnerAccountIndex int64  `json:"owner_account_index"`
	InitialBaseAmount Fixed  `json:"initial_base_amount"`
	Price             Fixed  `json:"price"`
	Nonce             int64  `json:"nonce"`
	RemainingBaseAmnt Fixed  `json:"remaining_base_amount"`
	IsAsk             bool   `json:"is_ask"`
	FilledBaseAmount  Fixed  `json:"filled_base_amount"`
	FilledQuoteAmount Fixed  `json:"filled_quote_amount"`
	// Type — view name ("limit", "market", "stop-loss", ...); compare with
	// OrderType.String().
	Type string `json:"type"`
	// TimeInForce — view name ("good-till-time", ...); compare with
	// TimeInForce.String().
	TimeInForce   string        `json:"time_in_force"`
	ReduceOnly    bool          `json:"reduce_only"`
	TriggerPrice  Fixed         `json:"trigger_price"`
	OrderExpiryMs int64         `json:"order_expiry"`
	Status        OrderStatus   `json:"status"`
	TriggerStatus TriggerStatus `json:"trigger_status"`
	TriggerTime   int64         `json:"trigger_time"`
	ParentOrderIx int64         `json:"parent_order_index"`
	ParentOrderID string        `json:"parent_order_id"`
	BlockHeight   int64         `json:"block_height"`
	// TimestampMs — order timestamp in milliseconds.
	TimestampMs int64 `json:"timestamp"`
	CreatedAtMs int64 `json:"created_at"`
	UpdatedAtMs int64 `json:"updated_at"`
	// TransactionTimeUs — sequencer time in microseconds.
	TransactionTimeUs int64 `json:"transaction_time"`
	// OrderVersion — current version (see ModifyOrderRequest.OrderVersion).
	OrderVersion int64 `json:"order_version"`
}

// RemainingBaseAmount returns the unfilled base amount.
func (o *Order) RemainingBaseAmount() Fixed { return o.RemainingBaseAmnt }

// IsActive reports whether the order rests or waits for a trigger.
func (o *Order) IsActive() bool { return o.Status.IsActive() }
