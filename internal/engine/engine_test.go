/*
FILE: internal/engine/engine_test.go

DESCRIPTION:
Engine tests against an in-process mock of the REST API (no network): nonce
lanes end to end (sync, sequential increment, rollback on an API rejection,
resync on a nonce error, unknown outcome on a transport failure), the sendTx
form body and its signature, batches, the auth header of private reads and
the SDK-side rate-limit accounting.
*/

package engine

import (
	"context"
	"encoding/base64"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tonymontanov/go-lighter/internal/codec"
	"github.com/tonymontanov/go-lighter/internal/lterr"
	"github.com/tonymontanov/go-lighter/internal/nonce"
	"github.com/tonymontanov/go-lighter/internal/ratelimit"
	"github.com/tonymontanov/go-lighter/internal/rest"
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/internal/tx"
	"github.com/tonymontanov/go-lighter/types"
)

// testPrivateKey — the fixed test key of the SDK test-suite.
const testPrivateKey string = "0x0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f202122232425262728"

type mockAPI struct {
	srv      *httptest.Server
	mu       sync.Mutex
	nextNonc int64
	nonceHit int
	sends    []url.Values
	sendResp func(form url.Values) (int, string)
	authSeen []string
}

func newMockAPI(t *testing.T) *mockAPI {
	var m = &mockAPI{nextNonc: 100}
	m.sendResp = func(form url.Values) (int, string) {
		return 200, `{"code":200,"tx_hash":"hash-` + form.Get("tx_type") + `","predicted_execution_time_ms":1751465474,"volume_quota_remaining":7}`
	}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.authSeen = append(m.authSeen, r.Header.Get("Authorization"))
		switch r.URL.Path {
		case "/api/v1/nextNonce":
			m.nonceHit++
			_, _ = io.WriteString(w, `{"code":200,"nonce":`+strconv.FormatInt(m.nextNonc, 10)+`}`)
		case "/api/v1/sendTx", "/api/v1/sendTxBatch":
			var raw, _ = io.ReadAll(r.Body)
			var form, err = url.ParseQuery(string(raw))
			if err != nil {
				http.Error(w, "bad form", 400)
				return
			}
			m.sends = append(m.sends, form)
			var status, body = m.sendResp(form)
			w.WriteHeader(status)
			_, _ = io.WriteString(w, body)
		case "/api/v1/account":
			_, _ = io.WriteString(w, `{"code":200,"total":1,"accounts":[{"index":1}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockAPI) lastSend() url.Values {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sends[len(m.sends)-1]
}

func newEngine(t *testing.T, m *mockAPI, mode nonce.Mode, events *[]ratelimit.Event) *Engine {
	t.Helper()
	var signer, err = signing.NewSigner(testPrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	var observer func(ratelimit.Event)
	if events != nil {
		observer = func(e ratelimit.Event) { *events = append(*events, e) }
	}
	return New(Config{
		REST:         rest.NewClient(rest.Config{BaseURL: m.srv.URL, RequestTimeout: time.Second}, nil),
		AccountIndex: 1,
		Keys:         []Key{{Index: 2, Signer: signer}},
		ChainID:      304,
		NonceMode:    mode,
		TxExpiry:     10 * time.Minute,
		Limiter:      ratelimit.New(ratelimit.Config{Tier: ratelimit.TierStandard, Observer: observer}),
	})
}

func orderTx(cli int64) *tx.CreateOrder {
	return &tx.CreateOrder{Order: tx.OrderInfo{MarketIndex: 0, ClientOrderIndex: cli, BaseAmount: 1000, Price: 405000, IsAsk: 0, Type: types.OrderTypeLimit, TimeInForce: types.TimeInForceGTT, OrderExpiry: 1800000000000}}
}

func TestSendSequentialNonces(t *testing.T) {
	var m = newMockAPI(t)
	var events []ratelimit.Event
	var e = newEngine(t, m, nonce.ModeSequential, &events)
	var ctx = context.Background()
	if !e.CanSign() || e.DefaultAPIKeyIndex() != 2 || e.AccountIndex() != 1 || e.ChainID() != 304 || len(e.APIKeyIndexes()) != 1 {
		t.Fatal("engine state")
	}

	var receipt, err = e.Send(ctx, orderTx(1), SendOptions{Category: ratelimit.CategoryPlace, PriceProtection: true})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Nonce != 100 || receipt.APIKeyIndex != 2 || receipt.TxHash != "hash-14" || receipt.PredictedExecutionTimeMs != 1751465474 || receipt.VolumeQuotaRemaining != 7 || receipt.TxType != types.TxTypeL2CreateOrder {
		t.Fatalf("receipt = %+v", receipt)
	}
	var form = m.lastSend()
	if form.Get("tx_type") != "14" || form.Get("price_protection") != "true" {
		t.Fatalf("form = %v", form)
	}
	// The tx_info is the reference shape, signed by the configured key.
	var info = form.Get("tx_info")
	var decoded struct {
		AccountIndex int64  `json:"AccountIndex"`
		ApiKeyIndex  uint8  `json:"ApiKeyIndex"`
		Nonce        int64  `json:"Nonce"`
		ExpiredAt    int64  `json:"ExpiredAt"`
		Sig          string `json:"Sig"`
		Attrs        *struct {
			Skip int64 `json:"4"`
		} `json:"L2TxAttributes"`
	}
	if err = codec.Unmarshal([]byte(info), &decoded); err != nil {
		t.Fatalf("tx_info is not JSON: %v\n%s", err, info)
	}
	if decoded.AccountIndex != 1 || decoded.ApiKeyIndex != 2 || decoded.Nonce != 100 || decoded.Attrs != nil {
		t.Fatalf("tx_info header = %+v", decoded)
	}
	if decoded.ExpiredAt < time.Now().Add(9*time.Minute).UnixMilli() || decoded.ExpiredAt > time.Now().Add(11*time.Minute).UnixMilli() {
		t.Fatalf("ExpiredAt = %d", decoded.ExpiredAt)
	}
	var sigBytes, sigErr = base64.StdEncoding.DecodeString(decoded.Sig)
	if sigErr != nil || len(sigBytes) != signing.SignatureLength {
		t.Fatalf("signature: %v %d", sigErr, len(sigBytes))
	}
	var rebuilt = orderTx(1)
	rebuilt.Header = tx.Header{AccountIndex: 1, APIKeyIndex: 2, ExpiredAt: decoded.ExpiredAt, Nonce: 100}
	var hash signing.Hash
	rebuilt.Hash(304, &hash)
	var sig signing.Signature
	copy(sig[:], sigBytes)
	var signer, _ = signing.NewSigner(testPrivateKey)
	var pk = signer.PublicKey()
	if !signing.Verify(&pk, &hash, &sig) {
		t.Fatal("signature does not verify against the rebuilt hash")
	}

	receipt, err = e.Send(ctx, orderTx(2), SendOptions{})
	if err != nil || receipt.Nonce != 101 {
		t.Fatalf("second send: %v %+v", err, receipt)
	}
	if m.nonceHit != 1 {
		t.Fatalf("nextNonce must be fetched once, got %d", m.nonceHit)
	}
	// API-level rejection: the nonce is reused.
	m.sendResp = func(form url.Values) (int, string) {
		return 400, `{"code":21739,"message":"not enough margin to create the order"}`
	}
	_, err = e.Send(ctx, orderTx(3), SendOptions{})
	if !lterr.IsExchange(err) || lterr.CodeOf(err) != 21739 {
		t.Fatalf("rejection: %v", err)
	}
	if e.Lane(2).Peek() != 102 {
		t.Fatalf("rejected nonce must be reused, peek=%d", e.Lane(2).Peek())
	}
	// Nonce error: resync from the exchange.
	m.sendResp = func(form url.Values) (int, string) { return 400, `{"code":21104,"message":"invalid nonce"}` }
	m.nextNonc = 500
	_, err = e.Send(ctx, orderTx(4), SendOptions{})
	if !lterr.IsNonceError(err) || e.Lane(2).Peek() != -1 {
		t.Fatalf("nonce error: %v peek=%d", err, e.Lane(2).Peek())
	}
	m.sendResp = func(form url.Values) (int, string) { return 200, `{"code":200,"tx_hash":"h"}` }
	receipt, err = e.Send(ctx, orderTx(5), SendOptions{})
	if err != nil || receipt.Nonce != 500 || m.nonceHit != 2 {
		t.Fatalf("resync: %v %+v hits=%d", err, receipt, m.nonceHit)
	}
	// Explicit nonce bypasses the lane.
	receipt, err = e.Send(ctx, orderTx(6), SendOptions{Nonce: 900})
	if err != nil || receipt.Nonce != 900 || e.Lane(2).Peek() != 501 {
		t.Fatalf("explicit nonce: %v %+v peek=%d", err, receipt, e.Lane(2).Peek())
	}
	// Local validation never reaches the wire and never consumes a nonce.
	var sends = len(m.sends)
	_, err = e.Send(ctx, &tx.CreateOrder{Order: tx.OrderInfo{MarketIndex: 0, BaseAmount: 1, Price: 0}}, SendOptions{})
	if !lterr.IsInvalidRequest(err) || len(m.sends) != sends || e.Lane(2).Peek() != 501 {
		t.Fatalf("validation: %v", err)
	}
	// Rate-limit events were emitted for every send.
	var sendEvents int
	for _, ev := range events {
		if ev.Endpoint == ratelimit.EndpointSendTx {
			sendEvents++
			if ev.TxType != types.TxTypeL2CreateOrder || ev.TxCount != 1 {
				t.Fatalf("event = %+v", ev)
			}
		}
	}
	if sendEvents != 6 {
		t.Fatalf("sendTx events = %d", sendEvents)
	}
}

func TestSendSkipMode(t *testing.T) {
	var m = newMockAPI(t)
	var e = newEngine(t, m, nonce.ModeSkip, nil)
	var receipt, err = e.Send(context.Background(), orderTx(1), SendOptions{})
	if err != nil || receipt.Nonce < 1_600_000_000_000 {
		t.Fatalf("skip mode: %v %+v", err, receipt)
	}
	if !strings.Contains(m.lastSend().Get("tx_info"), `"L2TxAttributes":{"4":1}`) {
		t.Fatalf("SkipNonce attribute missing: %s", m.lastSend().Get("tx_info"))
	}
	if m.nonceHit != 0 {
		t.Fatal("skip mode must not fetch nextNonce")
	}
}

func TestSendBatch(t *testing.T) {
	var m = newMockAPI(t)
	m.sendResp = func(form url.Values) (int, string) {
		return 200, `{"code":200,"tx_hash":["a","b"],"predicted_execution_time_ms":5}`
	}
	var e = newEngine(t, m, nonce.ModeSequential, nil)
	var receipt, err = e.SendBatch(context.Background(), []tx.Tx{orderTx(1), &tx.CancelOrder{MarketIndex: 0, Index: 1}}, SendOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if receipt.FirstNonce != 100 || receipt.Count != 2 || len(receipt.TxHashes) != 2 || receipt.TxHashes[1] != "b" || receipt.PredictedExecutionTimeMs != 5 {
		t.Fatalf("batch receipt = %+v", receipt)
	}
	var form = m.lastSend()
	if form.Get("tx_types") != "[14,15]" {
		t.Fatalf("tx_types = %s", form.Get("tx_types"))
	}
	var infos []string
	if err = codec.Unmarshal([]byte(form.Get("tx_infos")), &infos); err != nil || len(infos) != 2 {
		t.Fatalf("tx_infos = %s (%v)", form.Get("tx_infos"), err)
	}
	if !strings.Contains(infos[0], `"Nonce":100`) || !strings.Contains(infos[1], `"Nonce":101`) || !strings.HasPrefix(infos[1], `{"AccountIndex":1,"ApiKeyIndex":2,"MarketIndex":0,"Index":1,`) {
		t.Fatalf("batch infos = %v", infos)
	}
	if e.Lane(2).Peek() != 102 {
		t.Fatalf("batch must consume two nonces, peek=%d", e.Lane(2).Peek())
	}
	if _, err = e.SendBatch(context.Background(), nil, SendOptions{}); !lterr.IsInvalidRequest(err) {
		t.Fatalf("empty batch: %v", err)
	}
	var many = make([]tx.Tx, MaxBatchSizeWS+1)
	for i := range many {
		many[i] = orderTx(int64(i + 1))
	}
	if _, err = e.SendBatch(context.Background(), many, SendOptions{Transport: TransportWS}); !lterr.IsInvalidRequest(err) {
		t.Fatalf("ws batch too large: %v", err)
	}
}

func TestQueryAuthAndTransportFailure(t *testing.T) {
	var m = newMockAPI(t)
	var e = newEngine(t, m, nonce.ModeSequential, nil)
	var ctx = context.Background()
	var out struct {
		Total int `json:"total"`
	}
	if err := e.Query(ctx, "account", "by=index&value=1", true, &out, ratelimit.CategoryQuery); err != nil || out.Total != 1 {
		t.Fatalf("private query: %v %+v", err, out)
	}
	m.mu.Lock()
	var auth = m.authSeen[len(m.authSeen)-1]
	m.mu.Unlock()
	if !strings.HasPrefix(auth, strconv.FormatInt(time.Now().Add(7*time.Hour).Unix()/100, 10)) || !strings.Contains(auth, ":1:2:") {
		t.Fatalf("auth header = %q", auth)
	}
	var token1, _ = e.AuthToken()
	var token2, _ = e.AuthToken()
	if token1 != token2 {
		t.Fatal("auth token must be cached")
	}
	if err := e.Query(ctx, "account", "", false, nil, ratelimit.CategoryQuery); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	auth = m.authSeen[len(m.authSeen)-1]
	m.mu.Unlock()
	if auth != "" {
		t.Fatal("public query must not send the auth header")
	}
	if err := e.Query(ctx, "missing", "", false, nil, ratelimit.CategoryQuery); !lterr.IsInvalidRequest(err) {
		t.Fatalf("404: %v", err)
	}
	// Transport failure during a send: unknown outcome → the lane resyncs.
	m.srv.CloseClientConnections()
	m.srv.Close()
	if _, err := e.Send(ctx, orderTx(1), SendOptions{}); !lterr.IsNetwork(err) {
		t.Fatalf("transport failure: %v", err)
	}
	if e.Lane(2).Peek() != -1 {
		t.Fatalf("unknown outcome must invalidate the lane, peek=%d", e.Lane(2).Peek())
	}
}

func TestReadOnlyEngine(t *testing.T) {
	var m = newMockAPI(t)
	var e = New(Config{REST: rest.NewClient(rest.Config{BaseURL: m.srv.URL, RequestTimeout: time.Second}, nil), AccountIndex: 1, ChainID: 304})
	if e.CanSign() || e.DefaultAPIKeyIndex() != 0 || e.Lane(2) != nil || e.PublicKeyHex(2) != "" {
		t.Fatal("read-only engine state")
	}
	if _, err := e.Send(context.Background(), orderTx(1), SendOptions{}); !lterr.IsAuth(err) {
		t.Fatalf("send without key: %v", err)
	}
	if _, err := e.AuthToken(); !lterr.IsAuth(err) {
		t.Fatalf("auth token without key: %v", err)
	}
	if err := e.Query(context.Background(), "account", "by=index&value=1", false, nil, ratelimit.CategoryQuery); err != nil {
		t.Fatalf("public reads must work: %v", err)
	}
	if _, err := e.Send(context.Background(), orderTx(1), SendOptions{APIKeyIndex: 9}); !lterr.IsAuth(err) {
		t.Fatalf("unknown key without any key: %v", err)
	}
}

func TestRejectWhenRateLimited(t *testing.T) {
	var m = newMockAPI(t)
	var signer, _ = signing.NewSigner(testPrivateKey)
	var limiter = ratelimit.New(ratelimit.Config{Tier: ratelimit.TierStandard})
	var e = New(Config{
		REST: rest.NewClient(rest.Config{BaseURL: m.srv.URL, RequestTimeout: time.Second}, nil), AccountIndex: 1,
		Keys: []Key{{Index: 2, Signer: signer}}, ChainID: 304, Limiter: limiter, RejectWhenRateLimited: true,
	})
	var i int
	for i = 0; i < 60; i++ {
		limiter.AccountREST("account", 200, ratelimit.CategoryQuery)
	}
	if _, err := e.Send(context.Background(), orderTx(1), SendOptions{}); !lterr.IsRateLimit(err) {
		t.Fatalf("local guard: %v", err)
	}
	if err := e.Query(context.Background(), "account", "", false, nil, ratelimit.CategoryQuery); !lterr.IsRateLimit(err) {
		t.Fatalf("local guard on reads: %v", err)
	}
	if len(m.sends) != 0 {
		t.Fatal("nothing must reach the wire")
	}
}

func BenchmarkSendPrepare(b *testing.B) {
	// Measures the local part of Send (fill + hash + sign + tx_info + form)
	// without the network: the REST client points at a closed server and the
	// nonce is explicit, so the benchmark isolates the SDK's own work.
	var signer, _ = signing.NewSigner(testPrivateKey)
	var e = New(Config{REST: rest.NewClient(rest.Config{BaseURL: "http://127.0.0.1:1", RequestTimeout: time.Millisecond}, nil), AccountIndex: 1, Keys: []Key{{Index: 2, Signer: signer}}, ChainID: 304})
	var kl = e.byIndex[2]
	var order = orderTx(1)
	var opts = SendOptions{Nonce: 5}
	var p prepared
	var buf = make([]byte, 0, 2048)
	var form = make([]byte, 0, 4096)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = e.fill(order, kl, 5, &opts)
		_ = e.sign(order, kl, &p)
		buf = order.AppendInfo(buf[:0], &p.sig)
		form = rest.AppendSendTxForm(form[:0], 14, buf, true)
	}
}
