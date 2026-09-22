/*
FILE: internal/domain/trading.go

DESCRIPTION:
Unified trading functions of the common layer (see profile.go for the rule).
Every function validates locally, resolves the symbol through the section's
registry, scales prices and sizes to the market's wire integers, builds the
section-agnostic transaction (internal/tx) and hands it to the engine.

VALIDATION BEFORE NETWORK:
A rejected transaction still costs a round trip and rate-limit budget, so
everything that is a deterministic function of the payload is checked
locally and returned as ErrorKindInvalidRequest without any I/O: unknown /
inactive market, price or size off the grid or outside the wire range,
type / tif / expiry combinations (the reference signer's matrix). The SDK
does NOT round silently: callers normalise with types.Precision
(NormalizePrice / NormalizeSize) and choose the rounding direction.

ORDER EXPIRY DEFAULTS:
OrderExpiryMs == 0 → for IOC / market orders nothing is sent (0); for every
other kind now + defaultOrderExpiry (28 days, the official SDK default).

MARKET ORDERS:
Type MARKET + TimeInForce IOC + expiry 0; Price is the worst acceptable
price ("if the sequencer cannot offer an equal or better price, the order is
cancelled"). The section's MarketData helpers compute it from the book.
*/

package domain

import (
	"context"
	"strconv"
	"time"

	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/internal/ratelimit"
	"github.com/tonymontanov/go-lighter/internal/tx"
	"github.com/tonymontanov/go-lighter/types"
)

// DefaultOrderExpiry — order_expiry applied when the request carries none
// (the official SDK's 28 days; the exchange allows 5 minutes .. 30 days).
const DefaultOrderExpiry time.Duration = 28 * 24 * time.Hour

// sendOptions converts public options to engine options.
func sendOptions(options types.SendOptions, transport engine.Transport, category string) engine.SendOptions {
	return engine.SendOptions{
		Transport:       transport,
		APIKeyIndex:     options.APIKeyIndex,
		Nonce:           options.Nonce,
		ExpiredAtMs:     options.ExpiredAtMs,
		SkipNonce:       options.SkipNonce,
		PriceProtection: !options.DisablePriceProtection,
		Category:        category,
	}
}

// boolByte converts a bool to the 0 / 1 wire byte.
func boolByte(v bool) uint8 {
	if v {
		return 1
	}
	return 0
}

// orderInvalid builds the error of one order of a batch. The message is
// assembled only on the failure path — the success path allocates nothing here.
func (p *Profile) orderInvalid(operation string, index int, symbol string, detail string) error {
	return p.invalid(operation, "order["+strconv.Itoa(index)+"] "+symbol+": "+detail)
}

// requireActive rejects inactive markets before any I/O.
func (p *Profile) requireActive(operation string, index int, info *types.MarketInfo) error {
	if info.Status != "" && info.Status != types.MarketStatusActive {
		return p.orderInvalid(operation, index, info.Symbol, "market is "+string(info.Status))
	}
	return nil
}

// orderWire validates one request and converts it to its wire form.
func (p *Profile) orderWire(ctx context.Context, operation string, index int, req *types.CreateOrderRequest, out *tx.OrderInfo) error {
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, req.Symbol)
	if err != nil {
		return err
	}
	err = p.requireActive(operation, index, info)
	if err != nil {
		return err
	}
	if !req.Type.Valid() {
		return p.orderInvalid(operation, index, req.Symbol, "invalid order type")
	}
	if !req.TimeInForce.Valid() {
		return p.orderInvalid(operation, index, req.Symbol, "invalid time in force")
	}
	if req.ClientOrderIndex < 0 || req.ClientOrderIndex > tx.MaxClientOrderIndex {
		return p.orderInvalid(operation, index, req.Symbol, "client order index is outside 0..2^48-1")
	}

	var price uint32
	var ok bool
	price, ok = info.Precision.WirePrice(req.Price)
	if !ok {
		return p.orderInvalid(operation, index, req.Symbol, "price "+req.Price.String()+" is off the grid of "+strconv.Itoa(info.Precision.PriceDecimals)+" decimals or outside the wire range")
	}
	var size int64
	if req.Size == 0 && req.ReduceOnly {
		size = tx.NilOrderBaseAmount
	} else {
		size, ok = info.Precision.WireSize(req.Size)
		if !ok {
			return p.orderInvalid(operation, index, req.Symbol, "size "+req.Size.String()+" is off the grid of "+strconv.Itoa(info.Precision.SizeDecimals)+" decimals or outside the wire range")
		}
	}
	var trigger uint32
	if req.TriggerPrice != 0 {
		trigger, ok = info.Precision.WirePrice(req.TriggerPrice)
		if !ok {
			return p.orderInvalid(operation, index, req.Symbol, "trigger price "+req.TriggerPrice.String()+" is off the grid or outside the wire range")
		}
	}
	var expiry int64 = req.OrderExpiryMs
	var immediate bool = req.Type == types.OrderTypeMarket || (req.Type == types.OrderTypeLimit && req.TimeInForce == types.TimeInForceIOC)
	if expiry == 0 && !immediate {
		expiry = time.Now().Add(DefaultOrderExpiry).UnixMilli()
	}
	if expiry != 0 && !immediate {
		var now int64 = time.Now().UnixMilli()
		if expiry < now+tx.MinOrderExpiryPeriodMs || expiry > now+tx.MaxOrderExpiryPeriodMs {
			return p.orderInvalid(operation, index, req.Symbol, "order expiry must be 5 minutes .. 30 days ahead")
		}
	}

	out.MarketIndex = info.MarketID
	out.ClientOrderIndex = req.ClientOrderIndex
	out.BaseAmount = size
	out.Price = price
	out.IsAsk = boolByte(req.IsAsk)
	out.Type = req.Type
	out.TimeInForce = req.TimeInForce
	out.ReduceOnly = boolByte(req.ReduceOnly)
	out.TriggerPrice = trigger
	out.OrderExpiry = expiry
	return nil
}

// CreateOrder places one order (transaction type 14).
func CreateOrder(ctx context.Context, e *engine.Engine, p *Profile, request types.CreateOrderRequest, options types.SendOptions, transport engine.Transport) (types.TxReceipt, error) {
	const operation string = "CreateOrder"
	var t tx.CreateOrder
	var err error = p.orderWire(ctx, operation, 0, &request, &t.Order)
	if err != nil {
		return types.TxReceipt{}, err
	}
	return e.Send(ctx, &t, sendOptions(options, transport, ratelimit.CategoryPlace))
}

// CreateBatchOrders places several orders in one sendTxBatch (one nonce per
// order, same key). Every order is validated before anything is sent.
func CreateBatchOrders(ctx context.Context, e *engine.Engine, p *Profile, requests []types.CreateOrderRequest, options types.SendOptions, transport engine.Transport) (types.BatchReceipt, error) {
	const operation string = "CreateBatchOrders"
	if len(requests) == 0 {
		return types.BatchReceipt{}, p.invalid(operation, "empty batch")
	}
	var txs []tx.Tx = make([]tx.Tx, len(requests))
	var orders []tx.CreateOrder = make([]tx.CreateOrder, len(requests))
	var i int
	for i = 0; i < len(requests); i++ {
		var err error = p.orderWire(ctx, operation, i, &requests[i], &orders[i].Order)
		if err != nil {
			return types.BatchReceipt{}, err
		}
		txs[i] = &orders[i]
	}
	return e.SendBatch(ctx, txs, sendOptions(options, transport, ratelimit.CategoryPlace))
}

// modifyWire validates a modify request and fills the transaction.
func (p *Profile) modifyWire(ctx context.Context, operation string, index int, req *types.ModifyOrderRequest, out *tx.ModifyOrder) error {
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, req.Symbol)
	if err != nil {
		return err
	}
	if req.OrderIndex <= 0 {
		return p.orderInvalid(operation, index, req.Symbol, "order index is not set")
	}
	var price uint32
	var ok bool
	price, ok = info.Precision.WirePrice(req.Price)
	if !ok {
		return p.orderInvalid(operation, index, req.Symbol, "price "+req.Price.String()+" is off the grid or outside the wire range")
	}
	var size int64
	if req.Size != 0 {
		size, ok = info.Precision.WireSize(req.Size)
		if !ok {
			return p.orderInvalid(operation, index, req.Symbol, "size "+req.Size.String()+" is off the grid or outside the wire range")
		}
	}
	var trigger uint32
	if req.TriggerPrice != 0 {
		trigger, ok = info.Precision.WirePrice(req.TriggerPrice)
		if !ok {
			return p.orderInvalid(operation, index, req.Symbol, "trigger price is off the grid or outside the wire range")
		}
	}
	if req.OrderVersion < 0 {
		return p.orderInvalid(operation, index, req.Symbol, "order version is negative")
	}
	out.MarketIndex = info.MarketID
	out.Index = req.OrderIndex
	out.BaseAmount = size
	out.Price = price
	out.TriggerPrice = trigger
	out.Attributes.OrderVersion = req.OrderVersion
	return nil
}

// ModifyOrder replaces price / size / trigger of a resting order (type 17).
func ModifyOrder(ctx context.Context, e *engine.Engine, p *Profile, request types.ModifyOrderRequest, options types.SendOptions, transport engine.Transport) (types.TxReceipt, error) {
	const operation string = "ModifyOrder"
	var t tx.ModifyOrder
	var err error = p.modifyWire(ctx, operation, 0, &request, &t)
	if err != nil {
		return types.TxReceipt{}, err
	}
	return e.Send(ctx, &t, sendOptions(options, transport, ratelimit.CategoryAmend))
}

// ModifyBatchOrders replaces several resting orders in one sendTxBatch.
func ModifyBatchOrders(ctx context.Context, e *engine.Engine, p *Profile, requests []types.ModifyOrderRequest, options types.SendOptions, transport engine.Transport) (types.BatchReceipt, error) {
	const operation string = "ModifyBatchOrders"
	if len(requests) == 0 {
		return types.BatchReceipt{}, p.invalid(operation, "empty batch")
	}
	var txs []tx.Tx = make([]tx.Tx, len(requests))
	var modifies []tx.ModifyOrder = make([]tx.ModifyOrder, len(requests))
	var i int
	for i = 0; i < len(requests); i++ {
		var err error = p.modifyWire(ctx, operation, i, &requests[i], &modifies[i])
		if err != nil {
			return types.BatchReceipt{}, err
		}
		txs[i] = &modifies[i]
	}
	return e.SendBatch(ctx, txs, sendOptions(options, transport, ratelimit.CategoryAmend))
}

// cancelWire validates a cancel request and fills the transaction.
func (p *Profile) cancelWire(ctx context.Context, operation string, index int, req *types.CancelOrderRequest, out *tx.CancelOrder) error {
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, req.Symbol)
	if err != nil {
		return err
	}
	if req.OrderIndex <= 0 {
		return p.orderInvalid(operation, index, req.Symbol, "order index is not set")
	}
	out.MarketIndex = info.MarketID
	out.Index = req.OrderIndex
	return nil
}

// CancelOrder cancels one order by client order index or order index (type 15).
func CancelOrder(ctx context.Context, e *engine.Engine, p *Profile, request types.CancelOrderRequest, options types.SendOptions, transport engine.Transport) (types.TxReceipt, error) {
	const operation string = "CancelOrder"
	var t tx.CancelOrder
	var err error = p.cancelWire(ctx, operation, 0, &request, &t)
	if err != nil {
		return types.TxReceipt{}, err
	}
	return e.Send(ctx, &t, sendOptions(options, transport, ratelimit.CategoryCancel))
}

// CancelBatchOrders cancels several orders in one sendTxBatch.
func CancelBatchOrders(ctx context.Context, e *engine.Engine, p *Profile, requests []types.CancelOrderRequest, options types.SendOptions, transport engine.Transport) (types.BatchReceipt, error) {
	const operation string = "CancelBatchOrders"
	if len(requests) == 0 {
		return types.BatchReceipt{}, p.invalid(operation, "empty batch")
	}
	var txs []tx.Tx = make([]tx.Tx, len(requests))
	var cancels []tx.CancelOrder = make([]tx.CancelOrder, len(requests))
	var i int
	for i = 0; i < len(requests); i++ {
		var err error = p.cancelWire(ctx, operation, i, &requests[i], &cancels[i])
		if err != nil {
			return types.BatchReceipt{}, err
		}
		txs[i] = &cancels[i]
	}
	return e.SendBatch(ctx, txs, sendOptions(options, transport, ratelimit.CategoryCancel))
}

/*
CancelAllOrders cancels every order of the account (type 16), optionally
limited to one market (attribute 5, immediate mode only), or arms / disarms
the dead man's switch (scheduled / abort).
*/
func CancelAllOrders(ctx context.Context, e *engine.Engine, p *Profile, request types.CancelAllOrdersRequest, options types.SendOptions, transport engine.Transport) (types.TxReceipt, error) {
	const operation string = "CancelAllOrders"
	if !request.TimeInForce.Valid() {
		return types.TxReceipt{}, p.invalid(operation, "invalid time in force")
	}
	var t tx.CancelAllOrders
	t.TimeInForce = request.TimeInForce
	t.Time = request.TimeMs
	if request.Symbol != "" {
		if request.TimeInForce != types.CancelAllImmediate {
			return types.TxReceipt{}, p.invalid(operation, "a market-scoped cancel-all must be immediate")
		}
		var info *types.MarketInfo
		var err error
		info, err = p.resolve(ctx, operation, request.Symbol)
		if err != nil {
			return types.TxReceipt{}, err
		}
		t.Attributes.HasCancelAllMarket = true
		t.Attributes.CancelAllMarketIndex = info.MarketID
	}
	if request.TimeInForce == types.CancelAllScheduled {
		var now int64 = time.Now().UnixMilli()
		if request.TimeMs < now+tx.MinOrderCancelAllPeriodMs || request.TimeMs > now+tx.MaxOrderCancelAllPeriodMs {
			return types.TxReceipt{}, p.invalid(operation, "scheduled time must be 5 minutes .. 15 days ahead")
		}
	}
	return e.Send(ctx, &t, sendOptions(options, transport, ratelimit.CategoryCancel))
}

/*
UpdateLeverage sets the leverage of a market (type 20): the initial margin
fraction is 10000 / leverage, bounded below by the market's
min_initial_margin_fraction (the maximum leverage).
*/
func UpdateLeverage(ctx context.Context, e *engine.Engine, p *Profile, symbol string, leverage int, marginMode types.MarginMode, options types.SendOptions, transport engine.Transport) (types.TxReceipt, error) {
	const operation string = "UpdateLeverage"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return types.TxReceipt{}, err
	}
	if leverage < 1 {
		return types.TxReceipt{}, p.invalid(operation, symbol+": leverage must be at least 1")
	}
	if !marginMode.Valid() {
		return types.TxReceipt{}, p.invalid(operation, "invalid margin mode")
	}
	var fraction int64 = tx.MarginFractionTick / int64(leverage)
	if fraction < int64(info.MinInitialMarginFraction) {
		return types.TxReceipt{}, p.invalid(operation, symbol+": leverage "+strconv.Itoa(leverage)+" exceeds the market maximum "+strconv.Itoa(info.MaxLeverage()))
	}
	var t tx.UpdateLeverage = tx.UpdateLeverage{MarketIndex: info.MarketID, InitialMarginFraction: uint16(fraction), MarginMode: marginMode}
	return e.Send(ctx, &t, sendOptions(options, transport, ratelimit.CategoryOther))
}

/*
UpdateMargin adds (add == true) or removes USDC from the isolated margin of a
position (type 29). usdc is the amount in USDC (6 decimals on the wire).
*/
func UpdateMargin(ctx context.Context, e *engine.Engine, p *Profile, symbol string, usdc types.Fixed, add bool, options types.SendOptions, transport engine.Transport) (types.TxReceipt, error) {
	const operation string = "UpdateMargin"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return types.TxReceipt{}, err
	}
	var usdcPrecision types.Precision = types.Precision{PriceDecimals: 6, SizeDecimals: 6}
	var amount int64
	var ok bool
	amount, ok = usdcPrecision.WireSize(usdc)
	if !ok {
		return types.TxReceipt{}, p.invalid(operation, "amount must be positive with at most 6 decimals")
	}
	var direction uint8 = tx.MarginDirectionRemove
	if add {
		direction = tx.MarginDirectionAdd
	}
	var t tx.UpdateMargin = tx.UpdateMargin{MarketIndex: info.MarketID, USDCAmount: amount, Direction: direction}
	return e.Send(ctx, &t, sendOptions(options, transport, ratelimit.CategoryOther))
}
