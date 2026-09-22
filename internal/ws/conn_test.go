package ws

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// mockServer — scriptable /stream server: records client frames and answers
// subscribes with a snapshot + one update.
type mockServer struct {
	srv    *httptest.Server
	mu     sync.Mutex
	frames []string
	conns  int
	onText func(conn *websocket.Conn, frame string)
}

func newMockServer(t *testing.T) *mockServer {
	var m = &mockServer{}
	var upgrader = websocket.Upgrader{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var conn, err = upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		m.mu.Lock()
		m.conns++
		m.mu.Unlock()
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"connected"}`))
		for {
			var _, frame, readErr = conn.ReadMessage()
			if readErr != nil {
				return
			}
			m.mu.Lock()
			m.frames = append(m.frames, string(frame))
			var hook = m.onText
			m.mu.Unlock()
			if hook != nil {
				hook(conn, string(frame))
			}
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

func (m *mockServer) url() string { return "ws" + strings.TrimPrefix(m.srv.URL, "http") }

func (m *mockServer) recorded() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]string(nil), m.frames...)
}

func testConfig(url string) Config {
	return Config{
		URL:                     url,
		HandshakeTimeout:        time.Second,
		ReadTimeout:             2 * time.Second,
		WriteTimeout:            time.Second,
		PingInterval:            200 * time.Millisecond,
		ReconnectInitialBackoff: 20 * time.Millisecond,
		ReconnectMaxBackoff:     100 * time.Millisecond,
		ReconnectJitter:         0.1,
		ReadBufferSize:          4096,
		WriteBufferSize:         4096,
	}
}

func TestRouteKey(t *testing.T) {
	if RouteKey("order_book:0") != "order_book/0" || RouteKey("candle:0:1m") != "candle/0/1m" || RouteKey("market_stats/all") != "market_stats/all" {
		t.Fatal("RouteKey")
	}
	if string(subscriptionFrame("subscribe", "account_all_orders/5", "tok")) != `{"type":"subscribe","channel":"account_all_orders/5","auth":"tok"}` {
		t.Fatal("subscriptionFrame with auth")
	}
	if string(subscriptionFrame("unsubscribe", "order_book/0", "")) != `{"type":"unsubscribe","channel":"order_book/0"}` {
		t.Fatal("subscriptionFrame")
	}
}

func TestSubscribeDispatchAndPong(t *testing.T) {
	var m = newMockServer(t)
	m.onText = func(conn *websocket.Conn, frame string) {
		if strings.Contains(frame, `"subscribe"`) && strings.Contains(frame, "order_book/0") {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"subscribed/order_book","channel":"order_book:0","order_book":{"asks":[{"price":"1","size":"2"}],"bids":[]}}`))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"update/order_book","channel":"order_book:0","order_book":{"asks":[],"bids":[{"price":"0.9","size":"1"}]}}`))
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"ping"}`))
		}
		if strings.Contains(frame, "account_orders/3/77") {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"update/account_orders","channel":"account_orders:3","orders":{}}`))
		}
	}
	var c = NewConn(testConfig(m.url()), nil, nil)
	var ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	defer c.Close()

	var got = make(chan string, 8)
	var sub = &Subscription{Channel: "order_book/0", Handler: func(frame []byte, isSnapshot bool) {
		if isSnapshot {
			got <- "snapshot"
		} else {
			got <- "update"
		}
	}}
	if err := c.Subscribe(sub); err != nil {
		t.Fatal(err)
	}
	var authCalls int
	var private = &Subscription{Channel: "account_orders/3/77", Auth: func() (string, error) { authCalls++; return "tok", nil }, Handler: func(frame []byte, isSnapshot bool) {
		got <- "private"
	}}
	if err := c.Subscribe(private); err != nil {
		t.Fatal(err)
	}
	var expect = map[string]bool{"snapshot": false, "update": false, "private": false}
	var deadline = time.After(3 * time.Second)
	for i := 0; i < 3; i++ {
		select {
		case kind := <-got:
			expect[kind] = true
		case <-deadline:
			t.Fatalf("timed out waiting for pushes: %v", expect)
		}
	}
	for kind, seen := range expect {
		if !seen {
			t.Fatalf("missing push %s", kind)
		}
	}
	time.Sleep(300 * time.Millisecond)
	var frames = m.recorded()
	var sawPong, sawPing, sawAuth bool
	for _, f := range frames {
		if f == `{"type":"pong"}` {
			sawPong = true
		}
		if f == `{"type":"ping"}` {
			sawPing = true
		}
		if strings.Contains(f, `"auth":"tok"`) {
			sawAuth = true
		}
	}
	if !sawPong || !sawPing || !sawAuth || authCalls != 1 {
		t.Fatalf("pong=%v ping=%v auth=%v authCalls=%d frames=%v", sawPong, sawPing, sawAuth, authCalls, frames)
	}
	if !c.IsConnected() {
		t.Fatal("IsConnected")
	}
	if err := c.Unsubscribe(sub); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	var unsub bool
	for _, f := range m.recorded() {
		if f == `{"type":"unsubscribe","channel":"order_book/0"}` {
			unsub = true
		}
	}
	if !unsub {
		t.Fatal("unsubscribe frame not sent")
	}
}

func TestPostAndDisconnect(t *testing.T) {
	var m = newMockServer(t)
	m.onText = func(conn *websocket.Conn, frame string) {
		if strings.Contains(frame, `"type":"`+PostTypeSendTx+`"`) {
			if !strings.Contains(frame, `"data":{"id":"1","tx_type":14,"tx_info":{"a":1}}`) {
				_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"error","code":30007,"message":"Invalid Data"}`))
				return
			}
			// Live shape: no "type", the id at the top level.
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"code":200,"tx_hash":"abc","id":"1"}`))
		}
		if strings.Contains(frame, `"type":"`+PostTypeSendTxBatch+`"`) {
			_ = conn.Close()
		}
		if strings.Contains(frame, "nonexistent") {
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":{"code":20001,"message":"invalid param"}}`))
		}
	}
	var errText = make(chan string, 4)
	var cfg = testConfig(m.url())
	cfg.OnServerError = func(text string) { errText <- text }
	var c = NewConn(cfg, nil, nil)
	var ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	defer c.Close()
	if err := c.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	var res, err = c.Post(ctx, PostTypeSendTx, []byte(`"tx_type":14,"tx_info":{"a":1}`), time.Second)
	if err != nil || !strings.Contains(string(res.Frame), `"tx_hash":"abc"`) {
		t.Fatalf("post: %v %s", err, res.Frame)
	}
	_, err = c.Post(ctx, PostTypeSendTxBatch, []byte(`"tx_types":"[14]"`), time.Second)
	if err != ErrDisconnected {
		t.Fatalf("disconnect during post: %v", err)
	}
	// The supervisor reconnects; a post before that fails fast.
	_, err = c.Post(ctx, PostTypeSendTx, []byte(`"x":1`), time.Second)
	if err != ErrConnNotReady && err != nil && err != ErrDisconnected {
		t.Fatalf("post while reconnecting: %v", err)
	}
	if err = c.EnsureReady(ctx); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	var conns = m.conns
	m.mu.Unlock()
	if conns < 2 {
		t.Fatalf("expected a reconnect, conns=%d", conns)
	}
	if err = c.Subscribe(&Subscription{Channel: "nonexistent/1", Handler: func([]byte, bool) {}}); err != nil {
		t.Fatal(err)
	}
	select {
	case text := <-errText:
		if text != "invalid param" {
			t.Fatalf("error text = %q", text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("error frame not reported")
	}
	c.Close()
	if _, err = c.Post(ctx, PostTypeSendTx, []byte(`"x":1`), time.Second); err != ErrConnClosed {
		t.Fatalf("post after close: %v", err)
	}
	if err = c.Subscribe(&Subscription{Channel: "x", Handler: func([]byte, bool) {}}); err != ErrConnClosed {
		t.Fatalf("subscribe after close: %v", err)
	}
	if err = c.Subscribe(&Subscription{}); err != ErrInvalidSubscription {
		t.Fatalf("invalid subscription: %v", err)
	}
}

func TestResubscribeAndResetOnReconnect(t *testing.T) {
	var m = newMockServer(t)
	var subscribes = make(chan struct{}, 8)
	m.onText = func(conn *websocket.Conn, frame string) {
		if strings.Contains(frame, `"subscribe"`) {
			subscribes <- struct{}{}
			_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"subscribed/trade","channel":"trade:0","trades":[]}`))
			// Drop the connection after the first subscribe to force a reconnect.
			m.mu.Lock()
			var first = m.conns == 1
			m.mu.Unlock()
			if first {
				_ = conn.Close()
			}
		}
	}
	var c = NewConn(testConfig(m.url()), nil, nil)
	var ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	c.Start(ctx)
	defer c.Close()
	var resets int
	var mu sync.Mutex
	if err := c.Subscribe(&Subscription{Channel: "trade/0", Handler: func([]byte, bool) {}, Reset: func() { mu.Lock(); resets++; mu.Unlock() }}); err != nil {
		t.Fatal(err)
	}
	var deadline = time.After(3 * time.Second)
	for i := 0; i < 2; i++ {
		select {
		case <-subscribes:
		case <-deadline:
			t.Fatal("expected two subscribe frames (initial + resubscribe)")
		}
	}
	mu.Lock()
	var r = resets
	mu.Unlock()
	if r < 2 {
		t.Fatalf("Reset must fire on every connect, got %d", r)
	}
}
