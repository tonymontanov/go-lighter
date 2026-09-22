/*
FILE: perpetuals/trading.go

DESCRIPTION:
Order management of the Perpetuals section. Every method is the unified
function of the common layer (internal/domain) plus the section profile — no
request logic is implemented here.

TRANSPORT:
Trading() sends transactions over REST (sendTx / sendTxBatch); Trading().WS()
returns the same API routed through the WebSocket jsonapi (call
Client.WarmUpPost first so the first transaction does not pay for the
handshake). The WS variant never falls back to REST silently: with the socket
down a call fails fast with a Network error. The reply shape of the jsonapi
is only partially documented; treat the WS route as experimental until it
has been exercised on a live account.

RESULT CONVENTION:
  - err != nil          : the request failed locally or the API server
                          rejected it (nothing was forwarded; the nonce is
                          reused);
  - TxReceipt           : the API server accepted the transaction. Whether the
                          sequencer executed it arrives later on the
                          account_tx stream (types.TxOutcome, lighter.TxTracker)
                          and on the order streams.

EMULATED OPERATIONS:
  - CancelForgottenOrders : active orders older than a TTL → one batch cancel.
Cancel-all is native (transaction type 16, optionally scoped to one market).
*/

package perpetuals

import (
	"context"
	"time"

	"github.com/tonymontanov/go-lighter/internal/domain"
	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/types"
)

// TradingClient — order management sub-client.
type TradingClient struct {
	c         *Client
	transport engine.Transport
}

// WS returns the trading API routed through the WebSocket jsonapi.
func (t *TradingClient) WS() *TradingClient {
	return &TradingClient{c: t.c, transport: engine.TransportWS}
}

// CreateOrder places one order (limit / market / post-only / trigger kinds).
func (t *TradingClient) CreateOrder(ctx context.Context, request types.CreateOrderRequest, options types.SendOptions) (types.TxReceipt, error) {
	return domain.CreateOrder(ctx, t.c.engine(), t.c.prof(), request, options, t.transport)
}

// CreateBatchOrders places several orders in one sendTxBatch (at most 50
// over REST, 15 over WebSocket). One nonce per order, all on one key.
func (t *TradingClient) CreateBatchOrders(ctx context.Context, requests []types.CreateOrderRequest, options types.SendOptions) (types.BatchReceipt, error) {
	return domain.CreateBatchOrders(ctx, t.c.engine(), t.c.prof(), requests, options, t.transport)
}

// ModifyOrder replaces price / size / trigger of a resting order.
func (t *TradingClient) ModifyOrder(ctx context.Context, request types.ModifyOrderRequest, options types.SendOptions) (types.TxReceipt, error) {
	return domain.ModifyOrder(ctx, t.c.engine(), t.c.prof(), request, options, t.transport)
}

// ModifyBatchOrders replaces several resting orders in one sendTxBatch.
func (t *TradingClient) ModifyBatchOrders(ctx context.Context, requests []types.ModifyOrderRequest, options types.SendOptions) (types.BatchReceipt, error) {
	return domain.ModifyBatchOrders(ctx, t.c.engine(), t.c.prof(), requests, options, t.transport)
}

// CancelOrder cancels one order by client order index or order index. An
// order that is already gone is reported by the API server with a code for
// which lighter.IsMissingOrder returns true.
func (t *TradingClient) CancelOrder(ctx context.Context, request types.CancelOrderRequest, options types.SendOptions) (types.TxReceipt, error) {
	return domain.CancelOrder(ctx, t.c.engine(), t.c.prof(), request, options, t.transport)
}

// CancelBatchOrders cancels several orders in one sendTxBatch.
func (t *TradingClient) CancelBatchOrders(ctx context.Context, requests []types.CancelOrderRequest, options types.SendOptions) (types.BatchReceipt, error) {
	return domain.CancelBatchOrders(ctx, t.c.engine(), t.c.prof(), requests, options, t.transport)
}

// CancelAllOrders cancels every order of the account or of one market
// (immediate), or arms / disarms the dead man's switch (scheduled / abort).
func (t *TradingClient) CancelAllOrders(ctx context.Context, request types.CancelAllOrdersRequest, options types.SendOptions) (types.TxReceipt, error) {
	return domain.CancelAllOrders(ctx, t.c.engine(), t.c.prof(), request, options, t.transport)
}

// GetOpenOrders returns the active (resting / pending) orders of the
// section. symbol == "" returns every perp market.
func (t *TradingClient) GetOpenOrders(ctx context.Context, symbol string) ([]types.Order, error) {
	return domain.ActiveOrders(ctx, t.c.engine(), t.c.prof(), symbol)
}

// GetOrdersByClientIndex fetches orders by their client order indexes.
func (t *TradingClient) GetOrdersByClientIndex(ctx context.Context, clientOrderIndexes []int64) ([]types.Order, error) {
	return domain.OrdersByClientIndex(ctx, t.c.engine(), t.c.prof(), clientOrderIndexes)
}

// GetInactiveOrders returns a page of filled / cancelled orders, newest
// first (cursor == "" starts from the newest).
func (t *TradingClient) GetInactiveOrders(ctx context.Context, symbol string, limit int, cursor string) (domain.InactiveOrdersPage, error) {
	return domain.InactiveOrders(ctx, t.c.engine(), t.c.prof(), symbol, limit, cursor)
}

/*
CancelForgottenOrders cancels the active orders of symbol (""= every perp
market) older than ttl, in one batch, and returns the orders it tried to
cancel together with the batch receipt. Nothing is sent when no order is
old enough (an empty receipt is returned).
*/
func (t *TradingClient) CancelForgottenOrders(ctx context.Context, symbol string, ttl time.Duration, options types.SendOptions) ([]types.Order, types.BatchReceipt, error) {
	var orders []types.Order
	var err error
	orders, err = t.GetOpenOrders(ctx, symbol)
	if err != nil {
		return nil, types.BatchReceipt{}, err
	}
	var deadlineMs int64 = time.Now().Add(-ttl).UnixMilli()
	var stale []types.Order = orders[:0]
	var i int
	for i = 0; i < len(orders); i++ {
		if orders[i].TimestampMs <= deadlineMs {
			stale = append(stale, orders[i])
		}
	}
	if len(stale) == 0 {
		return stale, types.BatchReceipt{}, nil
	}
	var requests []types.CancelOrderRequest = make([]types.CancelOrderRequest, 0, len(stale))
	for i = 0; i < len(stale); i++ {
		var info *types.MarketInfo = t.c.prof().MarketByID(stale[i].MarketIndex)
		if info == nil {
			continue
		}
		requests = append(requests, types.CancelOrderRequest{Symbol: info.Symbol, OrderIndex: stale[i].OrderIndex})
	}
	var receipt types.BatchReceipt
	receipt, err = t.CancelBatchOrders(ctx, requests, options)
	return stale, receipt, err
}

// NextNonce fetches the next nonce of a configured key from the exchange
// (diagnostics; the SDK synchronises nonces itself).
func (t *TradingClient) NextNonce(ctx context.Context, apiKeyIndex uint8) (int64, error) {
	return t.c.engine().NextNonce(ctx, apiKeyIndex)
}
