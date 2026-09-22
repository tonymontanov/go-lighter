/*
FILE: perpetuals/contract_test.go

DESCRIPTION:
Contract tests of the Perpetuals section against an in-process mock of the
Lighter API (no network access). The mock serves the REST endpoints under
/api/v1 and a /stream WebSocket.

COVERED:
  - metadata → market ids / precision from orderBookDetails (perp filter);
  - the exact sendTx form body: tx_type, tx_info shape, signature recovery
    against the configured key, price scaling to the market grid;
  - market orders (IOC, no expiry), post-only default expiry, reduce-only
    with size 0, validation before network (off-grid price, unknown symbol,
    inactive market, market order with expiry);
  - cancel-all with market scope (attribute 5), batch cancel, modify with
    order version (attribute 8), leverage → initial margin fraction;
  - account reads (positions filtered to perp markets), auth header on
    private reads, missing-order classification;
  - streams: order book snapshot + delta + gap → resubscribe, orders
    filtered to perp markets, transactions → TxTracker.

FIXTURES:
Response fixtures are trimmed copies of live public mainnet answers captured
on 2026-09-22 and of the examples on the reference pages.
*/

package perpetuals

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	lighter "github.com/tonymontanov/go-lighter"
	"github.com/tonymontanov/go-lighter/internal/codec"
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/internal/tx"
	"github.com/tonymontanov/go-lighter/types"
)

// testPrivateKey — the fixed test key of the SDK test-suite. Not registered on any account.
const testPrivateKey string = "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728"

const fixtureMarkets string = `{"code":200,"order_book_details":[
 {"symbol":"ETH","market_id":0,"market_type":"perp","base_asset_id":0,"quote_asset_id":0,"status":"active","taker_fee":"0.0001","maker_fee":"0.0000","liquidation_fee":"0.01","min_base_amount":"0.0050","min_quote_amount":"10.000000","supported_size_decimals":4,"supported_price_decimals":2,"supported_quote_decimals":6,"size_decimals":4,"price_decimals":2,"quote_multiplier":1,"default_initial_margin_fraction":500,"min_initial_margin_fraction":200,"maintenance_margin_fraction":100,"closeout_margin_fraction":50,"last_trade_price":2728.9,"daily_trades_count":68,"daily_base_token_volume":235.25,"daily_quote_token_volume":93566.25,"daily_price_low":2700.1,"daily_price_high":2800.5,"daily_price_change":3.66,"open_interest":93.0,"mark_price":"2723.27","index_price":"2723.91","is_maker_fee_enabled":true,"is_taker_fee_enabled":true,"order_quote_limit":"281474976.710655","created_at":"1640995200000"},
 {"symbol":"BTC","market_id":1,"market_type":"perp","status":"active","taker_fee":"0.0001","maker_fee":"0.0000","min_base_amount":"0.00020","min_quote_amount":"10.000000","supported_size_decimals":5,"supported_price_decimals":1,"supported_quote_decimals":6,"size_decimals":5,"price_decimals":1,"default_initial_margin_fraction":500,"min_initial_margin_fraction":200,"maintenance_margin_fraction":100,"closeout_margin_fraction":50},
 {"symbol":"ZORA","market_id":53,"market_type":"perp","status":"inactive","min_base_amount":"700","min_quote_amount":"10.000000","supported_size_decimals":0,"supported_price_decimals":6,"supported_quote_decimals":6,"default_initial_margin_fraction":3333,"min_initial_margin_fraction":3333}
],"spot_order_book_details":[
 {"symbol":"ETH/USDC","market_id":2048,"market_type":"spot","status":"active","supported_size_decimals":4,"supported_price_decimals":2,"supported_quote_decimals":6}
]}`

const fixtureAccount string = `{"code":200,"total":1,"accounts":[{"code":0,"account_type":0,"index":1,"l1_address":"0x0000000000000000000000000000000000000000","cancel_all_time":0,"total_order_count":2,"pending_order_count":0,"available_balance":"1500.500000","status":0,"collateral":"2000.000000","account_index":1,"name":"","description":"","positions":[
 {"market_id":0,"symbol":"ETH","initial_margin_fraction":"500","open_order_count":2,"pending_order_count":0,"position_tied_order_count":0,"sign":-1,"position":"0.0335","avg_entry_price":"2986.30","position_value":"100.02","unrealized_pnl":"-0.0134","realized_pnl":"1.5","liquidation_price":"3500.00","margin_mode":0,"allocated_margin":"0"},
 {"market_id":2048,"symbol":"ETH/USDC","sign":1,"position":"1.0","avg_entry_price":"2000","position_value":"2000","unrealized_pnl":"0","realized_pnl":"0","liquidation_price":"0","margin_mode":0,"allocated_margin":"0"}
],"assets":[{"symbol":"USDC","asset_id":3,"balance":"10.000000","locked_balance":"0.000000","margin_mode":"disabled","margin_balance":"1.000000","multiplier":"1.000000000000000000"}],"total_asset_value":"2000","cross_asset_value":"2000","cross_initial_margin_requirement":"50.000000","cross_maintenance_margin_requirement":"10.000000","shares":[],"pending_unlocks":[]}]}`

const fixtureActiveOrders string = `{"code":200,"orders":[
 {"order_index":281477872907039,"client_order_index":11,"order_id":"281477872907039","client_order_id":"11","market_index":0,"owner_account_index":1,"initial_base_amount":"0.0100","price":"2000.00","nonce":5,"remaining_base_amount":"0.0100","is_ask":false,"filled_base_amount":"0.0000","filled_quote_amount":"0.000000","type":"limit","time_in_force":"post-only","reduce_only":false,"trigger_price":"0.00","order_expiry":1792506897590,"status":"open","trigger_status":"na","timestamp":1000,"order_version":0},
 {"order_index":281477872907040,"client_order_index":12,"order_id":"281477872907040","client_order_id":"12","market_index":2048,"owner_account_index":1,"initial_base_amount":"1.0000","price":"1000.00","remaining_base_amount":"1.0000","is_ask":true,"type":"limit","time_in_force":"good-till-time","status":"open","timestamp":1000}
]}`

const fixtureOrderBookOrders string = `{"code":200,"total_asks":3,"asks":[{"order_index":1,"order_id":"1","owner_account_index":5,"initial_base_amount":"1.0","remaining_base_amount":"1.0000","price":"2728.88","order_expiry":0,"transaction_time":0},{"order_index":2,"order_id":"2","owner_account_index":6,"initial_base_amount":"2.0","remaining_base_amount":"2.0000","price":"2728.88"},{"order_index":3,"order_id":"3","owner_account_index":7,"initial_base_amount":"0.5","remaining_base_amount":"0.5000","price":"2729.00"}],"total_bids":1,"bids":[{"order_index":4,"order_id":"4","owner_account_index":8,"initial_base_amount":"3","remaining_base_amount":"3.0000","price":"2728.50"}]}`

// mockExchange — scriptable Lighter mock.
type mockExchange struct {
	t         *testing.T
	srv       *httptest.Server
	mu        sync.Mutex
	rest      map[string]string
	calls     map[string]int
	auth      map[string]string
	sends     []url.Values
	sendResp  string
	sendCode  int
	wsFrames  []string
	onWsFrame func(conn *websocket.Conn, frame string)
}

func newMockExchange(t *testing.T) *mockExchange {
	var m = &mockExchange{
		t: t,
		rest: map[string]string{
			"orderBookDetails":    fixtureMarkets,
			"account":             fixtureAccount,
			"accountActiveOrders": fixtureActiveOrders,
			"orderBookOrders":     fixtureOrderBookOrders,
			"nextNonce":           `{"code":200,"nonce":42}`,
			"apikeys":             `{"code":200,"api_keys":[{"account_index":1,"api_key_index":2,"nonce":42,"public_key":"710c8cd2201061fa5570d20852d90ddabd6c496825b639d6c6486ecb1f859b63f237dee54f3f42b4"}]}`,
		},
		calls:    map[string]int{},
		auth:     map[string]string{},
		sendResp: `{"code":200,"tx_hash":"abc","predicted_execution_time_ms":1751465474,"volume_quota_remaining":0}`,
		sendCode: 200,
	}
	var upgrader = websocket.Upgrader{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/stream" {
			var conn, err = upgrader.Upgrade(w, r, nil)
			if err != nil {
				return
			}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"session_id":"x","type":"connected"}`))
			for {
				var _, frame, readErr = conn.ReadMessage()
				if readErr != nil {
					return
				}
				m.mu.Lock()
				m.wsFrames = append(m.wsFrames, string(frame))
				var hook = m.onWsFrame
				m.mu.Unlock()
				if hook != nil {
					hook(conn, string(frame))
				}
			}
		}
		var endpoint = strings.TrimPrefix(r.URL.Path, "/api/v1/")
		w.Header().Set("Content-Type", "application/json")
		m.mu.Lock()
		defer m.mu.Unlock()
		m.calls[endpoint]++
		m.auth[endpoint] = r.Header.Get("Authorization")
		switch endpoint {
		case "sendTx", "sendTxBatch":
			var raw, _ = io.ReadAll(r.Body)
			var form, _ = url.ParseQuery(string(raw))
			m.sends = append(m.sends, form)
			w.WriteHeader(m.sendCode)
			_, _ = io.WriteString(w, m.sendResp)
		default:
			var body, ok = m.rest[endpoint]
			if !ok {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `{"code":404,"message":"not found"}`)
				return
			}
			_, _ = io.WriteString(w, body)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockExchange) setSendResponse(code int, body string) {
	m.mu.Lock()
	m.sendCode = code
	m.sendResp = body
	m.mu.Unlock()
}

func (m *mockExchange) lastSend() url.Values {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sends) == 0 {
		return nil
	}
	return m.sends[len(m.sends)-1]
}

func (m *mockExchange) sendCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sends)
}

// newSection builds root client + section pointed at the mock.
func newSection(t *testing.T, m *mockExchange, mutate func(*lighter.Config)) (*lighter.Client, *Client) {
	t.Helper()
	var cfg = lighter.DefaultConfig()
	cfg.REST.BaseURL = m.srv.URL
	cfg.WS.URL = "ws" + strings.TrimPrefix(m.srv.URL, "http") + "/stream"
	cfg.WS.PingInterval = 100 * time.Millisecond
	cfg.WS.ReadTimeout = 2 * time.Second
	cfg.WS.ReconnectInitialBackoff = 20 * time.Millisecond
	cfg.WS.PostTimeout = time.Second
	cfg.AccountIndex = 1
	cfg.APIKeyIndex = 2
	cfg.PrivateKey = testPrivateKey
	cfg.MarketRefreshInterval = -1
	if mutate != nil {
		mutate(&cfg)
	}
	var root, err = lighter.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	return root, NewClient(root)
}

func fx(s string) types.Fixed { return types.MustParseFixed(s) }

// decodedInfo — the fields of a tx_info the tests look at.
type decodedInfo struct {
	AccountIndex     int64            `json:"AccountIndex"`
	ApiKeyIndex      uint8            `json:"ApiKeyIndex"`
	MarketIndex      int16            `json:"MarketIndex"`
	ClientOrderIndex int64            `json:"ClientOrderIndex"`
	BaseAmount       int64            `json:"BaseAmount"`
	Price            uint32           `json:"Price"`
	IsAsk            uint8            `json:"IsAsk"`
	Type             uint8            `json:"Type"`
	TimeInForce      uint8            `json:"TimeInForce"`
	ReduceOnly       uint8            `json:"ReduceOnly"`
	TriggerPrice     uint32           `json:"TriggerPrice"`
	OrderExpiry      int64            `json:"OrderExpiry"`
	Index            int64            `json:"Index"`
	Time             int64            `json:"Time"`
	IMF              uint16           `json:"InitialMarginFraction"`
	MarginMode       uint8            `json:"MarginMode"`
	ExpiredAt        int64            `json:"ExpiredAt"`
	Nonce            int64            `json:"Nonce"`
	Sig              string           `json:"Sig"`
	Attrs            codec.RawMessage `json:"L2TxAttributes"`
}

func decodeSend(t *testing.T, form url.Values) (uint8, decodedInfo) {
	t.Helper()
	var info decodedInfo
	if err := codec.Unmarshal([]byte(form.Get("tx_info")), &info); err != nil {
		t.Fatalf("tx_info is not JSON: %v\n%s", err, form.Get("tx_info"))
	}
	var txType = form.Get("tx_type")
	var code uint8
	for i := 0; i < len(txType); i++ {
		code = code*10 + uint8(txType[i]-'0')
	}
	return code, info
}

// verifySignature rebuilds the transaction hash from the decoded tx_info and
// checks the signature against the test key.
func verifySignature(t *testing.T, transaction tx.Tx, info decodedInfo) {
	t.Helper()
	var h = transaction.Head()
	h.AccountIndex = info.AccountIndex
	h.APIKeyIndex = info.ApiKeyIndex
	h.ExpiredAt = info.ExpiredAt
	h.Nonce = info.Nonce
	var hash signing.Hash
	transaction.Hash(lighter.MainnetChainID, &hash)
	var raw, err = base64.StdEncoding.DecodeString(info.Sig)
	if err != nil || len(raw) != signing.SignatureLength {
		t.Fatalf("signature: %v", err)
	}
	var sig signing.Signature
	copy(sig[:], raw)
	var signer, _ = signing.NewSigner(testPrivateKey)
	var pk = signer.PublicKey()
	if !signing.Verify(&pk, &hash, &sig) {
		t.Fatal("signature does not verify against the rebuilt hash")
	}
}

func TestMarketsFromDetails(t *testing.T) {
	var m = newMockExchange(t)
	var _, perps = newSection(t, m, nil)
	var ctx = context.Background()
	var list, err = perps.MarketData().GetMarkets(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 {
		t.Fatalf("markets = %d (spot markets must be filtered out)", len(list))
	}
	var eth, _ = perps.MarketData().GetMarketInfo(ctx, "ETH")
	if eth.MarketID != 0 || eth.Precision.PriceDecimals != 2 || eth.Precision.SizeDecimals != 4 || eth.MinBaseAmount != fx("0.005") || eth.MaxLeverage() != 50 || eth.TakerFee != fx("0.0001") {
		t.Fatalf("ETH = %+v", eth)
	}
	if _, err = perps.MarketData().GetMarketInfo(ctx, "ETH/USDC"); !lighter.IsInvalidRequest(err) {
		t.Fatalf("spot symbol must be unknown to the perp section: %v", err)
	}
	if m.calls["orderBookDetails"] != 1 {
		t.Fatalf("metadata must be loaded once, got %d", m.calls["orderBookDetails"])
	}
	var details, detailsErr = perps.MarketData().GetMarketDetails(ctx, "ETH")
	if detailsErr != nil || details.MarkPrice != fx("2723.27") || details.LastTradePrice != fx("2728.9") {
		t.Fatalf("details: %v %+v", detailsErr, details)
	}
	var book, bookErr = perps.MarketData().GetOrderBook(ctx, "ETH", 10)
	if bookErr != nil || len(book.Asks) != 2 || book.Asks[0].Size != fx("3") || book.Asks[0].Price != fx("2728.88") || len(book.Bids) != 1 {
		t.Fatalf("book: %v %+v", bookErr, book)
	}
	if p := SlippagePrice(eth.Precision, fx("2000"), false, 50); p != fx("2010") {
		t.Fatalf("SlippagePrice buy = %s", p)
	}
	if p := SlippagePrice(eth.Precision, fx("2000"), true, 50); p != fx("1990") {
		t.Fatalf("SlippagePrice sell = %s", p)
	}
}

func TestCreateOrderWire(t *testing.T) {
	var m = newMockExchange(t)
	var _, perps = newSection(t, m, nil)
	var ctx = context.Background()

	var receipt, err = perps.Trading().CreateOrder(ctx, types.CreateOrderRequest{
		Symbol: "ETH", ClientOrderIndex: 11, IsAsk: false, Price: fx("2000.00"), Size: fx("0.01"),
		Type: types.OrderTypeLimit, TimeInForce: types.TimeInForcePostOnly,
	}, types.SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.TxHash != "abc" || receipt.Nonce != 42 || receipt.APIKeyIndex != 2 || receipt.TxType != types.TxTypeL2CreateOrder {
		t.Fatalf("receipt = %+v", receipt)
	}
	var form = m.lastSend()
	var code, info = decodeSend(t, form)
	if code != 14 || info.MarketIndex != 0 || info.ClientOrderIndex != 11 || info.BaseAmount != 100 || info.Price != 200000 || info.IsAsk != 0 || info.Type != 0 || info.TimeInForce != 2 || info.ReduceOnly != 0 || info.TriggerPrice != 0 {
		t.Fatalf("tx_info = %+v", info)
	}
	var minExpiry = time.Now().Add(27 * 24 * time.Hour).UnixMilli()
	if info.OrderExpiry < minExpiry || string(info.Attrs) != "null" || form.Get("price_protection") != "true" {
		t.Fatalf("expiry / attrs / price protection: %+v %s", info, form.Get("price_protection"))
	}
	verifySignature(t, &tx.CreateOrder{Order: tx.OrderInfo{MarketIndex: 0, ClientOrderIndex: 11, BaseAmount: 100, Price: 200000, Type: types.OrderTypeLimit, TimeInForce: types.TimeInForcePostOnly, OrderExpiry: info.OrderExpiry}}, info)

	// Market order: IOC, no expiry, worst price; reduce-only with size 0.
	_, err = perps.Trading().CreateOrder(ctx, types.CreateOrderRequest{
		Symbol: "BTC", IsAsk: true, Price: fx("60000.0"), Size: 0, Type: types.OrderTypeMarket, TimeInForce: types.TimeInForceIOC, ReduceOnly: true,
	}, types.SendOptions{DisablePriceProtection: true})
	if err != nil {
		t.Fatal(err)
	}
	form = m.lastSend()
	code, info = decodeSend(t, form)
	if code != 14 || info.MarketIndex != 1 || info.BaseAmount != 0 || info.Price != 600000 || info.Type != 1 || info.TimeInForce != 0 || info.ReduceOnly != 1 || info.OrderExpiry != 0 || form.Get("price_protection") != "false" {
		t.Fatalf("market tx_info = %+v", info)
	}
	// Second nonce of the lane.
	if info.Nonce != 43 {
		t.Fatalf("nonce = %d", info.Nonce)
	}
}

func TestValidationBeforeNetwork(t *testing.T) {
	var m = newMockExchange(t)
	var _, perps = newSection(t, m, nil)
	var ctx = context.Background()
	var cases = []types.CreateOrderRequest{
		{Symbol: "ETH", Price: fx("2000.005"), Size: fx("0.01"), TimeInForce: types.TimeInForceGTT},                            // off-grid price
		{Symbol: "ETH", Price: fx("2000"), Size: fx("0.00001"), TimeInForce: types.TimeInForceGTT},                             // off-grid size
		{Symbol: "DOGE", Price: fx("1"), Size: fx("1"), TimeInForce: types.TimeInForceGTT},                                     // unknown symbol
		{Symbol: "ZORA", Price: fx("0.01"), Size: fx("1000"), TimeInForce: types.TimeInForceGTT},                               // inactive market
		{Symbol: "ETH", Price: fx("2000"), Size: fx("0.01"), Type: types.OrderTypeMarket, TimeInForce: types.TimeInForceGTT},   // market must be IOC
		{Symbol: "ETH", Price: fx("2000"), Size: fx("0.01"), TimeInForce: types.TimeInForceGTT, OrderExpiryMs: 5},              // expiry in the past
		{Symbol: "ETH", Price: fx("2000"), Size: 0, TimeInForce: types.TimeInForceGTT},                                         // zero size not reduce-only
		{Symbol: "ETH", Price: fx("2000"), Size: fx("0.01"), TimeInForce: 7},                                                   // bad tif
		{Symbol: "ETH", Price: fx("2000"), Size: fx("0.01"), Type: types.OrderTypeStopLoss, TimeInForce: types.TimeInForceIOC}, // trigger missing
	}
	for i, c := range cases {
		if _, err := perps.Trading().CreateOrder(ctx, c, types.SendOptions{}); !lighter.IsInvalidRequest(err) {
			t.Errorf("case %d: err = %v", i, err)
		}
	}
	if m.sendCount() != 0 {
		t.Fatal("nothing must reach the wire")
	}
	if _, err := perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: "ETH"}, types.SendOptions{}); !lighter.IsInvalidRequest(err) {
		t.Fatalf("cancel without index: %v", err)
	}
	if _, err := perps.Account().SetLeverage(ctx, "ETH", 100, types.MarginModeCross, types.SendOptions{}); !lighter.IsInvalidRequest(err) {
		t.Fatalf("leverage above the market maximum: %v", err)
	}
	if _, err := perps.Trading().CancelAllOrders(ctx, types.CancelAllOrdersRequest{Symbol: "ETH", TimeInForce: types.CancelAllScheduled, TimeMs: time.Now().Add(time.Hour).UnixMilli()}, types.SendOptions{}); !lighter.IsInvalidRequest(err) {
		t.Fatalf("scoped scheduled cancel-all: %v", err)
	}
}

func TestCancelModifyLeverageWire(t *testing.T) {
	var m = newMockExchange(t)
	var _, perps = newSection(t, m, nil)
	var ctx = context.Background()

	if _, err := perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: "ETH", OrderIndex: 11}, types.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	var code, info = decodeSend(t, m.lastSend())
	if code != 15 || info.MarketIndex != 0 || info.Index != 11 {
		t.Fatalf("cancel = %+v", info)
	}
	verifySignature(t, &tx.CancelOrder{MarketIndex: 0, Index: 11}, info)

	if _, err := perps.Trading().ModifyOrder(ctx, types.ModifyOrderRequest{Symbol: "ETH", OrderIndex: 11, Price: fx("2001.50"), Size: fx("0.02"), OrderVersion: 3}, types.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	code, info = decodeSend(t, m.lastSend())
	if code != 17 || info.Index != 11 || info.Price != 200150 || info.BaseAmount != 200 || string(info.Attrs) != `{"8":3}` {
		t.Fatalf("modify = %+v attrs=%s", info, info.Attrs)
	}
	verifySignature(t, &tx.ModifyOrder{Header: tx.Header{Attributes: tx.Attributes{OrderVersion: 3}}, MarketIndex: 0, Index: 11, BaseAmount: 200, Price: 200150}, info)

	if _, err := perps.Trading().CancelAllOrders(ctx, types.CancelAllOrdersRequest{Symbol: "BTC", TimeInForce: types.CancelAllImmediate}, types.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	code, info = decodeSend(t, m.lastSend())
	if code != 16 || info.Time != 0 || string(info.Attrs) != `{"5":1}` {
		t.Fatalf("cancel-all = %+v attrs=%s", info, info.Attrs)
	}
	if _, err := perps.Trading().CancelAllOrders(ctx, types.CancelAllOrdersRequest{TimeInForce: types.CancelAllScheduled, TimeMs: time.Now().Add(time.Hour).UnixMilli()}, types.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	code, info = decodeSend(t, m.lastSend())
	if code != 16 || info.Time == 0 || string(info.Attrs) != "null" {
		t.Fatalf("scheduled cancel-all = %+v", info)
	}

	if _, err := perps.Account().SetLeverage(ctx, "ETH", 20, types.MarginModeIsolated, types.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	code, info = decodeSend(t, m.lastSend())
	if code != 20 || info.IMF != 500 || info.MarginMode != 1 {
		t.Fatalf("leverage = %+v", info)
	}

	// Batch cancel: one form with two tx_infos and consecutive nonces.
	m.setSendResponse(200, `{"code":200,"tx_hash":["a","b"],"predicted_execution_time_ms":1}`)
	var receipt, err = perps.Trading().CancelBatchOrders(ctx, []types.CancelOrderRequest{{Symbol: "ETH", OrderIndex: 1}, {Symbol: "BTC", OrderIndex: 2}}, types.SendOptions{})
	if err != nil || receipt.Count != 2 || len(receipt.TxHashes) != 2 {
		t.Fatalf("batch: %v %+v", err, receipt)
	}
	var form = m.lastSend()
	if form.Get("tx_types") != "[15,15]" {
		t.Fatalf("tx_types = %s", form.Get("tx_types"))
	}
	var infos []string
	if unmarshalErr := codec.Unmarshal([]byte(form.Get("tx_infos")), &infos); unmarshalErr != nil || len(infos) != 2 || !strings.Contains(infos[1], `"MarketIndex":1,"Index":2`) {
		t.Fatalf("tx_infos = %s", form.Get("tx_infos"))
	}
}

func TestRejectionsAndMissingOrder(t *testing.T) {
	var m = newMockExchange(t)
	var _, perps = newSection(t, m, nil)
	var ctx = context.Background()
	m.setSendResponse(400, `{"code":21600,"message":"given order is not an active limit order"}`)
	var _, err = perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: "ETH", OrderIndex: 99}, types.SendOptions{})
	if !lighter.IsMissingOrder(err) || !lighter.IsInvalidRequest(err) || lighter.CodeOf(err) != lighter.CodeInactiveCancel {
		t.Fatalf("missing order: %v", err)
	}
	// The rejected nonce is reused by the next send.
	m.setSendResponse(200, `{"code":200,"tx_hash":"ok"}`)
	var receipt, sendErr = perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: "ETH", OrderIndex: 99}, types.SendOptions{})
	if sendErr != nil || receipt.Nonce != 42 {
		t.Fatalf("nonce reuse: %v %+v", sendErr, receipt)
	}
	m.setSendResponse(429, `{"code":23000,"message":"Too Many Requests!"}`)
	_, err = perps.Trading().CancelOrder(ctx, types.CancelOrderRequest{Symbol: "ETH", OrderIndex: 99}, types.SendOptions{})
	if !lighter.IsRateLimit(err) {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestAccountReads(t *testing.T) {
	var m = newMockExchange(t)
	var _, perps = newSection(t, m, nil)
	var ctx = context.Background()
	var positions, err = perps.Account().GetPositions(ctx)
	if err != nil || len(positions) != 1 || positions[0].Symbol != "ETH" || positions[0].SignedSize() != fx("-0.0335") || positions[0].IsFlat() {
		t.Fatalf("positions: %v %+v", err, positions)
	}
	var position, posErr = perps.Account().GetPosition(ctx, "BTC")
	if posErr != nil || !position.IsFlat() || position.MarketID != 1 {
		t.Fatalf("flat position: %v %+v", posErr, position)
	}
	var balance, balErr = perps.Account().GetBalance(ctx)
	if balErr != nil || balance.Collateral.String() != "2000" || balance.AvailableBalance.String() != "1500.5" {
		t.Fatalf("balance: %v %+v", balErr, balance)
	}
	var orders, ordErr = perps.Trading().GetOpenOrders(ctx, "")
	if ordErr != nil || len(orders) != 1 || orders[0].ClientOrderIndex != 11 || orders[0].Price != fx("2000") || !orders[0].IsActive() {
		t.Fatalf("open orders: %v %+v", ordErr, orders)
	}
	if m.auth["accountActiveOrders"] == "" || m.auth["account"] != "" {
		t.Fatalf("auth header: private=%q public=%q", m.auth["accountActiveOrders"], m.auth["account"])
	}
	if err = perps.Account().CheckAPIKey(ctx, 0); err != nil {
		t.Fatalf("CheckAPIKey: %v", err)
	}
	m.rest["apikeys"] = `{"code":200,"api_keys":[{"account_index":1,"api_key_index":2,"nonce":1,"public_key":"00"}]}`
	if err = perps.Account().CheckAPIKey(ctx, 2); !lighter.IsInvalidRequest(err) {
		t.Fatalf("CheckAPIKey mismatch: %v", err)
	}
	// ClosePosition: reduce-only market sell of the whole (short → buy) position.
	if _, err = perps.Account().ClosePosition(ctx, "ETH", fx("3100"), types.SendOptions{}); err != nil {
		t.Fatal(err)
	}
	var code, info = decodeSend(t, m.lastSend())
	if code != 14 || info.IsAsk != 0 || info.BaseAmount != 335 || info.ReduceOnly != 1 || info.Type != 1 {
		t.Fatalf("close = %+v", info)
	}
	// CancelForgottenOrders: the only perp order is old enough.
	m.setSendResponse(200, `{"code":200,"tx_hash":["x"]}`)
	var stale, _, forgottenErr = perps.Trading().CancelForgottenOrders(ctx, "", time.Second, types.SendOptions{})
	if forgottenErr != nil || len(stale) != 1 || m.lastSend().Get("tx_types") != "[15]" {
		t.Fatalf("forgotten: %v %d", forgottenErr, len(stale))
	}
}

func TestStreams(t *testing.T) {
	var m = newMockExchange(t)
	var bookSubscribes int
	m.onWsFrame = func(conn *websocket.Conn, frame string) {
		switch {
		case strings.Contains(frame, `"channel":"order_book/0"`) && strings.Contains(frame, `"type":"subscribe"`):
			m.mu.Lock()
			bookSubscribes++
			var first = bookSubscribes == 1
			m.mu.Unlock()
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"order_book:0","order_book":{"code":0,"asks":[{"price":"2723.04","size":"0.8850"},{"price":"2723.24","size":"1.6190"}],"bids":[{"price":"2722.92","size":"36.7254"}],"offset":1,"nonce":100,"begin_nonce":0},"timestamp":1,"type":"subscribed/order_book"}`))
			if !first {
				return
			}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"order_book:0","order_book":{"code":0,"asks":[{"price":"2723.04","size":"0.0000"}],"bids":[{"price":"2722.90","size":"1.0000"}],"offset":2,"nonce":110,"begin_nonce":100},"timestamp":2,"type":"update/order_book"}`))
			// Gap: begin_nonce 120 != 110 → the SDK must resubscribe (once).
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"order_book:0","order_book":{"code":0,"asks":[],"bids":[],"offset":3,"nonce":130,"begin_nonce":120},"timestamp":3,"type":"update/order_book"}`))
		case strings.Contains(frame, `"channel":"account_all_orders/1"`):
			if !strings.Contains(frame, `"auth":"`) {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":{"code":20001,"message":"auth field is required"}}`))
				return
			}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"account_all_orders:1","orders":{"0":[{"order_index":5,"client_order_index":11,"market_index":0,"status":"open","price":"2000.00","remaining_base_amount":"0.0100"}],"2048":[{"order_index":6,"market_index":2048,"status":"open"}]},"type":"update/account_all_orders"}`))
		case strings.Contains(frame, `"channel":"account_tx/1"`):
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"account_tx:1","txs":[{"hash":"abc","type":14,"status":2,"executed_at":5,"event_info":"{\"ae\":\"\"}"},{"hash":"def","type":15,"status":0,"event_info":"{\"a\":1,\"ae\":\"AppErrInvalidOrderIndex\"}"}],"type":"update/account_tx"}`))
		case strings.Contains(frame, `"channel":"ticker/0"`):
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"channel":"ticker:0","last_updated_at":1790087793439875,"nonce":22685517741,"ticker":{"s":"ETH","a":{"price":"2723.04","size":"0.7528"},"b":{"price":"2723.03","size":"0.6468"},"last_updated_at":1790087793439875},"timestamp":1790087793444,"type":"subscribed/ticker"}`))
		}
	}
	var root, perps = newSection(t, m, nil)
	var ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	if err := root.WarmUpStream(ctx); err != nil {
		t.Fatal(err)
	}

	var books = make(chan types.OrderBook, 8)
	var gaps = make(chan struct{}, 4)
	if err := perps.Stream().WatchOrderBook(ctx, "ETH", 0, func(b *types.OrderBook) {
		var cp = types.OrderBook{MarketID: b.MarketID, Nonce: b.Nonce, Asks: append([]types.OrderBookLevel(nil), b.Asks...), Bids: append([]types.OrderBookLevel(nil), b.Bids...)}
		select {
		case books <- cp:
		default:
		}
	}, func() {
		select {
		case gaps <- struct{}{}:
		default:
		}
	}, func(err error) { t.Error(err) }); err != nil {
		t.Fatal(err)
	}
	var first, second types.OrderBook
	select {
	case first = <-books:
	case <-time.After(3 * time.Second):
		t.Fatal("no snapshot")
	}
	select {
	case second = <-books:
	case <-time.After(3 * time.Second):
		t.Fatal("no update")
	}
	if first.Nonce != 100 || len(first.Asks) != 2 || second.Nonce != 110 || len(second.Asks) != 1 || len(second.Bids) != 2 || second.Bids[0].Price != fx("2722.92") {
		t.Fatalf("books: %+v %+v", first, second)
	}
	select {
	case <-gaps:
	case <-time.After(3 * time.Second):
		t.Fatal("gap not reported")
	}
	// The resubscribe yields a fresh snapshot (the mock answers every subscribe with one).
	select {
	case third := <-books:
		if third.Nonce != 100 || len(third.Asks) != 2 {
			t.Fatalf("resync snapshot: %+v", third)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no snapshot after the gap")
	}

	var orders = make(chan []types.Order, 4)
	if err := perps.Stream().WatchOrders(ctx, "", func(push *OrdersPush) {
		orders <- append([]types.Order(nil), push.Orders...)
	}, nil, func(err error) { t.Error(err) }); err != nil {
		t.Fatal(err)
	}
	select {
	case list := <-orders:
		if len(list) != 1 || list[0].ClientOrderIndex != 11 {
			t.Fatalf("orders must be filtered to perp markets: %+v", list)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no orders push")
	}

	var tracker = lighter.NewTxTracker(16)
	if err := perps.Stream().WatchTransactions(ctx, tracker.Observe, nil, func(err error) { t.Error(err) }); err != nil {
		t.Fatal(err)
	}
	var awaitCtx, awaitCancel = context.WithTimeout(ctx, 3*time.Second)
	defer awaitCancel()
	var outcome, err = tracker.Await(awaitCtx, "def")
	if err != nil || !outcome.Failed() || outcome.AppError != "AppErrInvalidOrderIndex" {
		t.Fatalf("outcome: %v %+v", err, outcome)
	}
	if ok, _ := tracker.Lookup("abc"); !ok.Executed() {
		t.Fatal("executed outcome")
	}

	var tickers = make(chan types.Ticker, 2)
	if err = perps.Stream().WatchTicker(ctx, "ETH", func(tk *types.Ticker) { tickers <- *tk }, func(err error) { t.Error(err) }); err != nil {
		t.Fatal(err)
	}
	select {
	case tk := <-tickers:
		if tk.Ask.Price != fx("2723.04") || tk.Bid.Size != fx("0.6468") || tk.Symbol != "ETH" || tk.Nonce != 22685517741 {
			t.Fatalf("ticker = %+v", tk)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no ticker")
	}
	if !perps.Stream().IsConnected() {
		t.Fatal("IsConnected")
	}
}
