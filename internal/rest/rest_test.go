package rest

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/tonymontanov/go-lighter/internal/lterr"
)

func TestQueryAndForms(t *testing.T) {
	var q Query
	q.Int("market_id", 0).Str("filter", "perp").Bool("active_only", true).Str("symbol", "ETH/USDC")
	if q.String() != "market_id=0&filter=perp&active_only=true&symbol=ETH%2FUSDC" {
		t.Fatalf("query = %s", q.String())
	}
	var info = []byte(`{"AccountIndex":1,"Sig":"a+b/c=","L2TxAttributes":null}`)
	var form = AppendSendTxForm(nil, 14, info, true)
	var want = "tx_type=14&tx_info=" + url.QueryEscape(string(info)) + "&price_protection=true"
	if string(form) != want {
		t.Fatalf("form\n got %s\nwant %s", form, want)
	}
	var batch, _ = AppendSendTxBatchForm(nil, nil, []uint8{14, 15}, [][]byte{info, []byte(`{"x":"y"}`)})
	var wantBatch = "tx_types=" + url.QueryEscape("[14,15]") + "&tx_infos=" + url.QueryEscape(`["{\"AccountIndex\":1,\"Sig\":\"a+b/c=\",\"L2TxAttributes\":null}","{\"x\":\"y\"}"]`)
	if string(batch) != wantBatch {
		t.Fatalf("batch form\n got %s\nwant %s", batch, wantBatch)
	}
	if string(AppendJSONTxTypes(nil, []uint8{14})) != "[14]" || string(AppendJSONTxInfos(nil, [][]byte{[]byte(`{"a":1}`)})) != `["{\"a\":1}"]` {
		t.Fatal("ws batch helpers")
	}
	if string(AppendEscapedString(nil, "a b~c")) != "a+b~c" {
		t.Fatal("escape")
	}
}

func TestClientErrors(t *testing.T) {
	var srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/ok":
			if r.Header.Get("Authorization") != "token" || r.URL.RawQuery != "a=1" {
				http.Error(w, "bad request headers", 400)
				return
			}
			_, _ = io.WriteString(w, `{"code":200,"nonce":5}`)
		case "/api/v1/app-error":
			w.WriteHeader(400)
			_, _ = io.WriteString(w, `{"code":21104,"message":"invalid nonce"}`)
		case "/api/v1/soft-error":
			_, _ = io.WriteString(w, `{"code":21602,"message":"invalid market index"}`)
		case "/api/v1/rate":
			w.WriteHeader(429)
			_, _ = io.WriteString(w, `{"code":23000,"message":"Too Many Requests!"}`)
		case "/api/v1/plain":
			w.WriteHeader(503)
			_, _ = io.WriteString(w, "upstream down")
		case "/api/v1/sendTx":
			var body, _ = io.ReadAll(r.Body)
			if r.Header.Get("Content-Type") != contentTypeForm || !strings.HasPrefix(string(body), "tx_type=14&tx_info=") {
				http.Error(w, "bad form", 400)
				return
			}
			_, _ = io.WriteString(w, `{"code":200,"tx_hash":"abc"}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	var c = NewClient(Config{BaseURL: srv.URL, RequestTimeout: time.Second, UserAgent: "test"}, nil)
	defer c.Close()
	var ctx = context.Background()

	var res, err = c.Get(ctx, "ok", "a=1", "token")
	if err != nil || res.Status != 200 || string(res.Body) != `{"code":200,"nonce":5}` {
		t.Fatalf("ok: %v %+v", err, res)
	}
	_, err = c.Get(ctx, "app-error", "", "")
	if !lterr.IsAuth(err) || lterr.CodeOf(err) != 21104 {
		t.Fatalf("app error: %v", err)
	}
	_, err = c.Get(ctx, "soft-error", "", "")
	if !lterr.IsInvalidRequest(err) || lterr.CodeOf(err) != 21602 {
		t.Fatalf("soft error: %v", err)
	}
	res, err = c.Get(ctx, "rate", "", "")
	if !lterr.IsRateLimit(err) || res.Status != 429 {
		t.Fatalf("rate: %v %d", err, res.Status)
	}
	_, err = c.Get(ctx, "plain", "", "")
	if !lterr.IsNetwork(err) || !strings.Contains(err.Error(), "upstream down") {
		t.Fatalf("plain: %v", err)
	}
	_, err = c.Get(ctx, "missing", "", "")
	if !lterr.IsInvalidRequest(err) {
		t.Fatalf("404: %v", err)
	}
	res, err = c.PostForm(ctx, "sendTx", AppendSendTxForm(nil, 14, []byte(`{"a":1}`), true), "")
	if err != nil || string(res.Body) != `{"code":200,"tx_hash":"abc"}` {
		t.Fatalf("sendTx: %v %s", err, res.Body)
	}
	var cancelled, cancel = context.WithCancel(ctx)
	cancel()
	if _, err = c.Get(cancelled, "ok", "", ""); !lterr.IsNetwork(err) {
		t.Fatalf("cancelled ctx: %v", err)
	}
	if c.BaseURL() != srv.URL {
		t.Fatal("BaseURL")
	}
}

func BenchmarkAppendSendTxForm(b *testing.B) {
	var info = []byte(`{"AccountIndex":1,"ApiKeyIndex":2,"MarketIndex":0,"ClientOrderIndex":1,"BaseAmount":1000,"Price":405000,"IsAsk":0,"Type":0,"TimeInForce":1,"ReduceOnly":0,"TriggerPrice":0,"OrderExpiry":1800000000000,"ExpiredAt":1700000000000,"Nonce":7,"Sig":"kjMRcB9NBKA+KkTgO2jBsbQO63xh0Xkxf/M7NTG8VnYWyV3I9vDYNo4fiWrUtuKHsjYDVZIaxjjgz3sVGMGJRDbY7m7iMHOn5ABt2YceTSc=","L2TxAttributes":null}`)
	var buf = make([]byte, 0, 1024)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = AppendSendTxForm(buf[:0], 14, info, true)
	}
}
