/*
FILE: internal/ws/conn.go

DESCRIPTION:
Supervised WebSocket connection to the Lighter /stream endpoint. One Conn
carries subscriptions AND jsonapi posts; the SDK opens a separate Conn for
posts when trading over WebSocket, so a burst of market data can never delay
the reply to an order (head-of-line blocking inside one TCP stream).

Private channels authenticate PER SUBSCRIPTION with an auth token (valid at
most 8 hours), so a subscription carries an Auth callback that is asked for a
fresh token every time the subscribe frame is sent (first subscribe and
every resubscribe after a reconnect).

RESPONSIBILITIES:
 1. supervise      : dial → run → on failure sleep (backoff * 2, capped, with
                     jitter) → redial, until ctx is cancelled or Close is called.
                     The docs warn that deployments may drop connections.
 2. subscriptions  : registry keyed by route key. Subscribing while
                     disconnected is legal — the registry is replayed on every
                     (re)connect, after calling each subscription's Reset hook.
 3. keepalive      : {"type":"ping"} every PingInterval (the server closes a
                     connection silent for 2 minutes); server pings are
                     answered with {"type":"pong"}; every received frame
                     refreshes the read deadline (silent-server detector).
 4. posts          : Post() correlates a jsonapi request with its reply by id.
                     Requests are NEVER buffered across reconnects: with the
                     socket down the call fails fast with ErrConnNotReady, and
                     every pending request fails with ErrDisconnected when the
                     socket dies — silently re-sending an order after a
                     reconnect would be dangerous.

CONTRACT OF Subscription.Handler:
  - called sequentially from the read loop of the connection, for the
    "subscribed/*" snapshot (isSnapshot == true) and every "update/*";
  - frame is the WHOLE server message and is only valid during the call
    (the buffer is reused for the next frame) — decode it or copy it;
  - must not block: a slow handler delays every other subscription.
CONTRACT OF Subscription.Reset:
  - called on the connection goroutine under c.mu before (re)subscribing: it
    must be fast and non-blocking (drop local state, signal a channel).

DEPENDENCIES:
- github.com/gorilla/websocket: WS client.
- internal/codec, internal/ltlog, internal/ltmet.
*/

package ws

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"

	"github.com/tonymontanov/go-lighter/internal/codec"
	"github.com/tonymontanov/go-lighter/internal/ltlog"
	"github.com/tonymontanov/go-lighter/internal/ltmet"
)

// Sentinel errors of the connection.
var (
	// ErrConnClosed — the Conn was closed permanently.
	ErrConnClosed error = errors.New("ws: connection closed")
	// ErrConnNotReady — no live socket right now (connecting / reconnecting).
	ErrConnNotReady error = errors.New("ws: connection is not ready")
	// ErrPostTimeout — no reply to a post within the timeout. The request MAY
	// still have been processed by the exchange.
	ErrPostTimeout error = errors.New("ws: post request timed out")
	// ErrDisconnected — the socket died while a post was pending. The request
	// MAY still have been processed by the exchange.
	ErrDisconnected error = errors.New("ws: disconnected while waiting for a post reply")
	// ErrInvalidSubscription — a Subscription is missing mandatory fields.
	ErrInvalidSubscription error = errors.New("ws: invalid subscription")
)

// Config — connection settings.
type Config struct {
	URL                     string
	HandshakeTimeout        time.Duration
	ReadTimeout             time.Duration
	WriteTimeout            time.Duration
	PingInterval            time.Duration
	ReconnectInitialBackoff time.Duration
	ReconnectMaxBackoff     time.Duration
	ReconnectJitter         float64
	ReadBufferSize          int
	WriteBufferSize         int
	// EnableCompression — negotiate permessage-deflate (supported by the
	// exchange). Off by default: it trades CPU and latency for bandwidth.
	EnableCompression bool
	// Proxy — optional proxy selector. nil → http.ProxyFromEnvironment.
	Proxy func(*http.Request) (*url.URL, error)
	// OnServerError — optional hook for error frames.
	OnServerError func(text string)
}

// Subscription — one registered subscription.
type Subscription struct {
	// Channel — subscription channel in the '/' form ("order_book/0").
	Channel string
	// Auth — optional token supplier of private channels; called every time
	// the subscribe frame is sent.
	Auth func() (string, error)
	// Handler — receives every push of the channel (see the file header).
	Handler func(frame []byte, isSnapshot bool)
	// Reset — optional; see the contract in the file header.
	Reset func()
}

// subscriptionGroup — every subscription sharing one route key. The exchange
// sees one subscription; members is copy-on-write.
type subscriptionGroup struct {
	channel string
	auth    func() (string, error)
	members []*Subscription
}

// PostResult — reply to a post request: the whole server frame (owned copy).
type PostResult struct {
	Frame []byte
}

// pendingPost — a post request waiting for its reply.
type pendingPost struct {
	reply chan PostResult
	err   chan error
}

// Conn — supervised WS connection. Safe for concurrent use.
type Conn struct {
	cfg    Config
	logger ltlog.Logger

	mu     sync.RWMutex
	subs   map[string]*subscriptionGroup
	socket *websocket.Conn
	closed bool

	writeMu   sync.Mutex
	startOnce sync.Once
	cancel    context.CancelFunc

	pendingMu sync.Mutex
	pending   map[string]pendingPost
	postSeq   atomic.Uint64

	cReceived    ltmet.Counter
	cDropped     ltmet.Counter
	cReconn      ltmet.Counter
	cSub         ltmet.Counter
	cPostTimeout ltmet.Counter
	cPostOrphan  ltmet.Counter
}

// NewConn creates a connection object. It performs no network I/O.
func NewConn(cfg Config, logger ltlog.Logger, metrics ltmet.CounterFactory) *Conn {
	if logger == nil {
		logger = ltlog.Noop()
	}
	if metrics == nil {
		metrics = ltmet.Noop()
	}
	return &Conn{
		cfg:          cfg,
		logger:       logger,
		subs:         make(map[string]*subscriptionGroup, 16),
		pending:      make(map[string]pendingPost, 16),
		cReceived:    metrics.Counter("lighter_ws_messages_received_total"),
		cDropped:     metrics.Counter("lighter_ws_messages_dropped_total"),
		cReconn:      metrics.Counter("lighter_ws_reconnects_total"),
		cSub:         metrics.Counter("lighter_ws_subscriptions_total"),
		cPostTimeout: metrics.Counter("lighter_ws_post_timeout_total"),
		cPostOrphan:  metrics.Counter("lighter_ws_post_orphan_total"),
	}
}

// Start launches the supervise loop. Idempotent: only the first call has
// effect. The connection lives until ctx is cancelled or Close is called.
func (c *Conn) Start(ctx context.Context) {
	c.startOnce.Do(func() {
		var superviseCtx context.Context
		var cancel context.CancelFunc
		superviseCtx, cancel = context.WithCancel(ctx)
		c.mu.Lock()
		c.cancel = cancel
		c.mu.Unlock()
		go c.supervise(superviseCtx)
	})
}

// Close terminates the connection permanently. Safe to call multiple times.
func (c *Conn) Close() {
	c.mu.Lock()
	c.closed = true
	var socket *websocket.Conn = c.socket
	var cancel context.CancelFunc = c.cancel
	c.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if socket != nil {
		_ = socket.Close()
	}
	c.failAllPending(ErrConnClosed)
}

// IsConnected reports whether a live socket exists right now.
func (c *Conn) IsConnected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.socket != nil
}

// EnsureReady blocks until the socket is live, ctx is done or the Conn is closed.
func (c *Conn) EnsureReady(ctx context.Context) error {
	var ticker *time.Ticker = time.NewTicker(2 * time.Millisecond)
	defer ticker.Stop()
	for {
		c.mu.RLock()
		var ready bool = c.socket != nil
		var closed bool = c.closed
		c.mu.RUnlock()
		if closed {
			return ErrConnClosed
		}
		if ready {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

/*
Subscribe registers a subscription. Buffered semantics: the registration is
stored first, so subscribing while disconnected is legal — it is applied on the
next (re)connect. If a socket is live, the subscribe frame is sent immediately.

SHARED KEYS: several subscriptions may share one channel (two consumers of the
trades of one market). The exchange is subscribed ONCE — by the first of them —
and every push is delivered to all handlers, in registration order.
*/
func (c *Conn) Subscribe(sub *Subscription) error {
	if sub == nil || sub.Channel == "" || sub.Handler == nil {
		return ErrInvalidSubscription
	}
	var key string = RouteKey(sub.Channel)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrConnClosed
	}
	var group *subscriptionGroup = c.subs[key]
	var first bool = group == nil
	if first {
		group = &subscriptionGroup{channel: sub.Channel, auth: sub.Auth}
		c.subs[key] = group
	}
	// Copy-on-write: dispatch iterates a slice header taken under RLock, so
	// the old backing array is never mutated — a fresh one replaces it.
	var members []*Subscription = make([]*Subscription, 0, len(group.members)+1)
	members = append(members, group.members...)
	members = append(members, sub)
	group.members = members
	var socket *websocket.Conn = c.socket
	c.mu.Unlock()

	if !first {
		return nil
	}
	c.cSub.Inc()
	if socket == nil {
		return nil // will subscribe on connect
	}
	return c.sendSubscribe(socket, group)
}

// sendSubscribe writes the subscribe frame of a group, fetching a fresh auth
// token when the channel is private.
func (c *Conn) sendSubscribe(socket *websocket.Conn, group *subscriptionGroup) error {
	var auth string
	if group.auth != nil {
		var err error
		auth, err = group.auth()
		if err != nil {
			return fmt.Errorf("ws: auth token for %s: %w", group.channel, err)
		}
	}
	return c.writeFrame(socket, subscriptionFrame(typeSubscribe, group.channel, auth))
}

// Unsubscribe removes ONE subscription (the pointer passed to Subscribe). The
// unsubscribe frame is sent when the last subscription of the channel is
// gone. Unknown subscriptions are a no-op.
func (c *Conn) Unsubscribe(sub *Subscription) error {
	if sub == nil {
		return nil
	}
	var key string = RouteKey(sub.Channel)
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return ErrConnClosed
	}
	var group *subscriptionGroup = c.subs[key]
	if group == nil {
		c.mu.Unlock()
		return nil
	}
	var members []*Subscription = make([]*Subscription, 0, len(group.members))
	var i int
	for i = 0; i < len(group.members); i++ {
		if group.members[i] != sub {
			members = append(members, group.members[i])
		}
	}
	group.members = members
	var last bool = len(members) == 0
	if last {
		delete(c.subs, key)
	}
	var socket *websocket.Conn = c.socket
	c.mu.Unlock()

	if !last || socket == nil {
		return nil
	}
	return c.writeFrame(socket, subscriptionFrame(typeUnsubscribe, group.channel, ""))
}

/*
Post sends a jsonapi request and waits for its reply.

msgType is PostTypeSendTx or PostTypeSendTxBatch; dataFields is the content
of the "data" object WITHOUT the surrounding braces and without the id
(e.g. `"tx_type":14,"tx_info":{...}`); the connection adds a unique id and
correlates the reply. The reply frame is returned as is: the caller owns the
mapping to SDK results because it knows what was sent.

Fails fast with ErrConnNotReady when there is no live socket (no buffering
across reconnects). On ErrPostTimeout / ErrDisconnected the outcome on the
exchange side is UNKNOWN.
*/
func (c *Conn) Post(ctx context.Context, msgType string, dataFields []byte, timeout time.Duration) (PostResult, error) {
	c.mu.RLock()
	var socket *websocket.Conn = c.socket
	var closed bool = c.closed
	c.mu.RUnlock()
	if closed {
		return PostResult{}, ErrConnClosed
	}
	if socket == nil {
		return PostResult{}, ErrConnNotReady
	}

	var seq uint64 = c.postSeq.Add(1)
	var idBuf [24]byte
	var id string = string(strconv.AppendUint(idBuf[:0], seq, 10))
	var waiter pendingPost = pendingPost{reply: make(chan PostResult, 1), err: make(chan error, 1)}

	// Register BEFORE writing: the reply can arrive before WriteMessage returns.
	c.pendingMu.Lock()
	c.pending[id] = waiter
	c.pendingMu.Unlock()

	var frame []byte = make([]byte, 0, len(dataFields)+len(msgType)+48)
	frame = append(frame, `{"type":"`...)
	frame = append(frame, msgType...)
	frame = append(frame, `","data":{"id":"`...)
	frame = append(frame, id...)
	frame = append(frame, '"', ',')
	frame = append(frame, dataFields...)
	frame = append(frame, '}', '}')

	var err error = c.writeFrame(socket, frame)
	if err != nil {
		c.dropPending(id)
		return PostResult{}, fmt.Errorf("ws: write post: %w", err)
	}

	var timer *time.Timer = time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case result := <-waiter.reply:
		return result, nil
	case err = <-waiter.err:
		return PostResult{}, err
	case <-timer.C:
		c.dropPending(id)
		c.cPostTimeout.Inc()
		return PostResult{}, ErrPostTimeout
	case <-ctx.Done():
		c.dropPending(id)
		return PostResult{}, ctx.Err()
	}
}

// dropPending forgets a pending post (timeout / cancellation / write error).
func (c *Conn) dropPending(id string) {
	c.pendingMu.Lock()
	delete(c.pending, id)
	c.pendingMu.Unlock()
}

// failAllPending fails every pending post with err.
func (c *Conn) failAllPending(err error) {
	c.pendingMu.Lock()
	var id string
	var waiter pendingPost
	for id, waiter = range c.pending {
		waiter.err <- err
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
}

// supervise — reconnect loop with backoff + jitter.
func (c *Conn) supervise(ctx context.Context) {
	var backoff time.Duration = c.cfg.ReconnectInitialBackoff
	for {
		if ctx.Err() != nil {
			return
		}
		var connected bool
		var err error
		connected, err = c.connectAndRun(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			c.logger.Warn("ws: connection error, will reconnect", ltlog.Err(err))
		}
		c.cReconn.Inc()
		if connected {
			// The previous attempt reached a live socket: start the next
			// series of attempts from the initial delay again.
			backoff = c.cfg.ReconnectInitialBackoff
		}

		var sleep time.Duration = applyJitter(backoff, c.cfg.ReconnectJitter)
		select {
		case <-ctx.Done():
			return
		case <-time.After(sleep):
		}
		backoff = nextBackoff(backoff, c.cfg.ReconnectMaxBackoff)
	}
}

// connectAndRun dials, replays subscriptions and runs the read loop until the
// socket dies or ctx is cancelled. connected reports whether the dial succeeded.
func (c *Conn) connectAndRun(ctx context.Context) (bool, error) {
	var proxy func(*http.Request) (*url.URL, error) = c.cfg.Proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	var dialer websocket.Dialer = websocket.Dialer{
		Proxy:             proxy,
		HandshakeTimeout:  c.cfg.HandshakeTimeout,
		ReadBufferSize:    c.cfg.ReadBufferSize,
		WriteBufferSize:   c.cfg.WriteBufferSize,
		EnableCompression: c.cfg.EnableCompression,
	}
	var socket *websocket.Conn
	var err error
	// The *http.Response of a successful upgrade carries no body to close: the
	// underlying connection IS the socket (gorilla/websocket contract).
	socket, _, err = dialer.DialContext(ctx, c.cfg.URL, nil) //nolint:bodyclose // upgrade response, see above
	if err != nil {
		return false, fmt.Errorf("dial: %w", err)
	}

	// Publish the socket and reset stateful subscribers BEFORE any frame of
	// the new connection is processed, so a reconnect cannot mix old and new
	// state.
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = socket.Close()
		return true, nil
	}
	c.socket = socket
	var groups []*subscriptionGroup = make([]*subscriptionGroup, 0, len(c.subs))
	var group *subscriptionGroup
	for _, group = range c.subs {
		var m int
		for m = 0; m < len(group.members); m++ {
			if group.members[m].Reset != nil {
				group.members[m].Reset()
			}
		}
		groups = append(groups, group)
	}
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.socket = nil
		c.mu.Unlock()
		_ = socket.Close()
		c.failAllPending(ErrDisconnected)
	}()

	var i int
	for i = 0; i < len(groups); i++ {
		err = c.sendSubscribe(socket, groups[i])
		if err != nil {
			return true, fmt.Errorf("resubscribe: %w", err)
		}
	}

	// loopCtx stops the ping loop and the ctx watcher when the read loop exits.
	var loopCtx context.Context
	var stopLoops context.CancelFunc
	loopCtx, stopLoops = context.WithCancel(ctx)
	defer stopLoops()

	go c.pingLoop(loopCtx, socket)
	go func() {
		// Close the socket when ctx is cancelled so the blocking ReadMessage
		// returns promptly. Also runs on normal exit, where Close is a no-op
		// repeated by the deferred cleanup above.
		<-loopCtx.Done()
		_ = socket.Close()
	}()

	return true, c.readLoop(socket)
}

// pingLoop sends the keepalive frame every PingInterval.
func (c *Conn) pingLoop(ctx context.Context, socket *websocket.Conn) {
	var ticker *time.Ticker = time.NewTicker(c.cfg.PingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var err error = c.writeFrame(socket, pingFrame)
			if err != nil {
				// A failed write means the socket is dead: closing it makes
				// the read loop return and the supervisor reconnect.
				_ = socket.Close()
				return
			}
		}
	}
}

// readLoop reads and dispatches frames until the socket dies.
func (c *Conn) readLoop(socket *websocket.Conn) error {
	var env envelope
	for {
		var err error = socket.SetReadDeadline(time.Now().Add(c.cfg.ReadTimeout))
		if err != nil {
			return err
		}
		var msgType int
		var frame []byte
		msgType, frame, err = socket.ReadMessage()
		if err != nil {
			return err
		}
		if msgType != websocket.TextMessage {
			continue
		}
		c.cReceived.Inc()

		env.Type = ""
		env.Channel = ""
		err = codec.Unmarshal(frame, &env)
		if err != nil {
			c.cDropped.Inc()
			continue
		}
		c.dispatch(socket, &env, frame)
	}
}

// dispatch routes one frame.
func (c *Conn) dispatch(socket *websocket.Conn, env *envelope, frame []byte) {
	switch {
	case env.Type == typePong, env.Type == typeConnected:
		return
	case env.Type == typePing:
		_ = c.writeFrame(socket, pongFrame)
		return
	case hasPrefix(env.Type, prefixPost):
		c.dispatchPost(frame)
		return
	case env.Type == "" && env.Channel == "" && (codec.GetString(frame, "id") != "" || codec.GetString(frame, "data", "id") != ""):
		// Observed live: jsonapi replies carry no "type", only the id —
		// {"error":{"code":21109,"message":"api key not found"},"id":"1"}.
		c.dispatchPost(frame)
		return
	case env.Type == typeError, env.Channel == "" && codec.GetInt(frame, "code") != 0, env.Type == "" && codec.GetInt(frame, "error", "code") != 0:
		// Observed live: {"error":{"code":20001,"message":"..."}} (no "type").
		var text string = codec.GetString(frame, "error", "message")
		if text == "" {
			text = codec.GetString(frame, "message")
		}
		if text == "" {
			text = string(frame)
		}
		c.logger.Warn("ws: server error frame", ltlog.Str("text", text))
		if c.cfg.OnServerError != nil {
			c.cfg.OnServerError(text)
		}
		return
	}

	var isSnapshot bool = hasPrefix(env.Type, prefixSubscribed)
	if !isSnapshot && !hasPrefix(env.Type, prefixUpdate) {
		c.cDropped.Inc()
		return
	}
	var members []*Subscription = c.lookup(RouteKey(env.Channel))
	if len(members) == 0 {
		c.cDropped.Inc()
		return
	}
	var i int
	for i = 0; i < len(members); i++ {
		members[i].Handler(frame, isSnapshot)
	}
}

// lookup finds the members of a route key: exact match first, then the
// group whose key extends the push key ("account_orders/0" matches the
// subscription "account_orders/0/123").
func (c *Conn) lookup(key string) []*Subscription {
	c.mu.RLock()
	defer c.mu.RUnlock()
	var group *subscriptionGroup = c.subs[key]
	if group != nil {
		return group.members
	}
	var candidate string
	for candidate, group = range c.subs {
		if len(candidate) > len(key) && candidate[:len(key)] == key && candidate[len(key)] == '/' {
			return group.members
		}
	}
	return nil
}

// dispatchPost delivers a post reply to its waiter. The id is looked up in
// "data.id" first, then at the top level (the reply shape is not documented).
func (c *Conn) dispatchPost(frame []byte) {
	var id string = codec.GetString(frame, "data", "id")
	if id == "" {
		id = codec.GetString(frame, "id")
	}
	c.pendingMu.Lock()
	var waiter pendingPost
	var ok bool
	waiter, ok = c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.pendingMu.Unlock()
	if !ok {
		// The caller already gave up (timeout / ctx) or the id is unknown.
		c.cPostOrphan.Inc()
		c.logger.Warn("ws: orphan post reply", ltlog.Str("id", id))
		return
	}
	var owned []byte = make([]byte, len(frame))
	copy(owned, frame)
	waiter.reply <- PostResult{Frame: owned}
}

// writeFrame writes one text frame under the write mutex.
func (c *Conn) writeFrame(socket *websocket.Conn, frame []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	var err error = socket.SetWriteDeadline(time.Now().Add(c.cfg.WriteTimeout))
	if err != nil {
		return err
	}
	return socket.WriteMessage(websocket.TextMessage, frame)
}

// applyJitter multiplies d by a random factor in [1-j, 1+j].
func applyJitter(d time.Duration, jitter float64) time.Duration {
	if jitter <= 0 {
		return d
	}
	var factor float64 = 1 + jitter*(2*rand.Float64()-1)
	return time.Duration(float64(d) * factor)
}

// nextBackoff doubles the backoff up to the max.
func nextBackoff(d time.Duration, maxBackoff time.Duration) time.Duration {
	d *= 2
	if d > maxBackoff {
		return maxBackoff
	}
	return d
}
