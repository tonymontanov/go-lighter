/*
FILE: client.go

DESCRIPTION:
The main public SDK Client: the public face of the COMMON LAYER. It owns the
shared resources (signing keys, REST transport, the unified engine, the
WebSocket connections, the rate-limit accounting) that every section
(perpetuals, spot, ...) is built on.

    lighter.NewClient(cfg)  →  *lighter.Client
    perpetuals.NewClient(client) → section with Trading / Account / MarketData / Stream

Sections are constructed by their own packages with a typed constructor (no
`any` + type assertion): the root never imports a section, so there is no
import cycle.

MAIN FUNCTIONS:
  - NewClient(cfg)        : constructor with Config validation and defaults.
  - (Client).Engine()     : the unified request layer (used by sections).
  - (Client).StreamConn() : shared WebSocket connection for subscriptions
                            (started lazily on first use).
  - (Client).PostConn()   : WebSocket connection for jsonapi transactions.
  - (Client).WarmUpStream / WarmUpPost : connect ahead of the first use.
  - (Client).Close()      : stops background work, zeroes the keys.

CONNECTION LIFETIME:
Both WebSocket connections belong to the Client and live until Close.
Subscriptions are shared between consumers (several Watch* of one channel
= one exchange subscription) and re-established after reconnects.
*/

package lighter

import (
	"context"
	"sync"

	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/internal/ltlog"
	"github.com/tonymontanov/go-lighter/internal/ratelimit"
	"github.com/tonymontanov/go-lighter/internal/rest"
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/internal/ws"
)

// Client — root SDK object. Safe for concurrent use.
type Client struct {
	cfg     Config
	signers []*signing.Signer
	rest    *rest.Client
	engine  *engine.Engine
	limiter *ratelimit.Limiter

	lifeCtx    context.Context
	lifeCancel context.CancelFunc

	streamOnce sync.Once
	streamConn *ws.Conn
	postOnce   sync.Once
	postConn   *ws.Conn
}

// NewClient creates the root SDK client. cfg goes through withDefaults +
// validate. When PrivateKey is set the client can sign transactions and
// read private data; otherwise it can only access public endpoints.
func NewClient(cfg Config) (*Client, error) {
	cfg = cfg.withDefaults()
	var err error = cfg.validate()
	if err != nil {
		return nil, err
	}

	var c *Client = &Client{cfg: cfg}
	c.lifeCtx, c.lifeCancel = context.WithCancel(context.Background())

	var keys []engine.Key
	if cfg.PrivateKey != "" {
		var signer *signing.Signer
		signer, err = signing.NewSigner(cfg.PrivateKey)
		if err != nil {
			c.lifeCancel()
			return nil, NewError(ErrorKindAuth, "config: PrivateKey", err)
		}
		c.signers = append(c.signers, signer)
		keys = append(keys, engine.Key{Index: cfg.APIKeyIndex, Signer: signer})
		var i int
		for i = 0; i < len(cfg.ExtraKeys); i++ {
			var extra *signing.Signer
			extra, err = signing.NewSigner(cfg.ExtraKeys[i].PrivateKey)
			if err != nil || !extra.Enabled() {
				c.Close()
				return nil, NewError(ErrorKindAuth, "config: ExtraKeys private key", err)
			}
			c.signers = append(c.signers, extra)
			keys = append(keys, engine.Key{Index: cfg.ExtraKeys[i].Index, Signer: extra})
		}
	}
	// The key material is not needed any more: keep it out of the config copy.
	c.cfg.PrivateKey = ""
	c.cfg.ExtraKeys = nil

	c.rest = rest.NewClient(rest.Config{
		BaseURL:             cfg.REST.BaseURL,
		RequestTimeout:      cfg.REST.RequestTimeout,
		MaxIdleConns:        cfg.REST.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.REST.MaxIdleConnsPerHost,
		IdleConnTimeout:     cfg.REST.IdleConnTimeout,
		UserAgent:           cfg.UserAgent,
		Proxy:               cfg.REST.Proxy,
		HTTPClient:          cfg.REST.HTTPClient,
	}, cfg.Logger)

	var observer func(ratelimit.Event)
	if cfg.RateLimitEventObserver != nil {
		observer = cfg.RateLimitEventObserver
	}
	c.limiter = ratelimit.New(ratelimit.Config{
		Tier:               ratelimit.ParseTier(cfg.RateLimit.Tier),
		StakedLIT:          cfg.RateLimit.StakedLIT,
		DefaultTxTypeLimit: cfg.RateLimit.DefaultTxTypeLimit,
		Observer:           observer,
	})

	c.engine = engine.New(engine.Config{
		REST:                  c.rest,
		PostConn:              c.PostConn,
		AccountIndex:          cfg.AccountIndex,
		Keys:                  keys,
		ChainID:               cfg.ChainID,
		NonceMode:             cfg.NonceMode,
		TxExpiry:              cfg.TxExpiry,
		AuthTokenLifetime:     cfg.AuthTokenLifetime,
		PostTimeout:           cfg.WS.PostTimeout,
		Limiter:               c.limiter,
		RejectWhenRateLimited: cfg.RateLimit.RejectWhenRateLimited,
		Logger:                cfg.Logger,
		Metrics:               cfg.Metrics,
	})
	return c, nil
}

// Config returns a copy of the effective configuration (defaults applied,
// key material removed).
func (c *Client) Config() Config { return c.cfg }

// Engine returns the unified request layer. Sections use it; applications
// normally do not need it.
func (c *Client) Engine() *engine.Engine { return c.engine }

// Logger returns the configured logger.
func (c *Client) Logger() Logger { return c.cfg.Logger }

// Metrics returns the configured counter factory.
func (c *Client) Metrics() CounterFactory { return c.cfg.Metrics }

// LifeContext returns the context that is cancelled by Close. Background
// work of sections (metadata refresh, streams) is bound to it.
func (c *Client) LifeContext() context.Context { return c.lifeCtx }

// CanSign reports whether the client holds a signing key.
func (c *Client) CanSign() bool { return c.engine.CanSign() }

// AccountIndex returns the configured account index.
func (c *Client) AccountIndex() int64 { return c.cfg.AccountIndex }

// APIKeyIndexes returns the indexes of the configured signing keys (the
// first one is the default).
func (c *Client) APIKeyIndexes() []uint8 { return c.engine.APIKeyIndexes() }

// PublicKeyHex returns the public key of a configured signing key, in the
// form the apikeys endpoint prints ("" when the index is not configured).
func (c *Client) PublicKeyHex(apiKeyIndex uint8) string { return c.engine.PublicKeyHex(apiKeyIndex) }

// IsTestnet reports whether the client targets the testnet chain.
func (c *Client) IsTestnet() bool { return c.cfg.ChainID == TestnetChainID }

// RateLimits returns the current SDK-side budgets.
func (c *Client) RateLimits() RateLimitSnapshot { return c.limiter.Snapshot() }

// AuthToken returns a cached authentication token of the default key (for
// callers that talk to the exchange outside the SDK).
func (c *Client) AuthToken() (string, error) { return c.engine.AuthToken() }

// wsConfig builds the connection settings from the public config.
func (c *Client) wsConfig() ws.Config {
	return ws.Config{
		URL:                     c.cfg.WS.URL,
		HandshakeTimeout:        c.cfg.WS.HandshakeTimeout,
		ReadTimeout:             c.cfg.WS.ReadTimeout,
		WriteTimeout:            c.cfg.WS.WriteTimeout,
		PingInterval:            c.cfg.WS.PingInterval,
		ReconnectInitialBackoff: c.cfg.WS.ReconnectInitialBackoff,
		ReconnectMaxBackoff:     c.cfg.WS.ReconnectMaxBackoff,
		ReconnectJitter:         c.cfg.WS.ReconnectJitter,
		ReadBufferSize:          c.cfg.WS.ReadBufferSize,
		WriteBufferSize:         c.cfg.WS.WriteBufferSize,
		EnableCompression:       c.cfg.WS.EnableCompression,
		Proxy:                   c.cfg.WS.Proxy,
		OnServerError: func(text string) {
			c.cfg.Logger.Warn("ws: server error", ltlog.Str("text", text))
		},
	}
}

// StreamConn returns the shared subscription connection, starting it on
// first use. It reconnects until Close.
func (c *Client) StreamConn() *ws.Conn {
	c.streamOnce.Do(func() {
		c.streamConn = ws.NewConn(c.wsConfig(), c.cfg.Logger, c.cfg.Metrics)
		c.streamConn.Start(c.lifeCtx)
	})
	return c.streamConn
}

// PostConn returns the connection used for jsonapi transactions, starting it
// on first use. Kept separate from StreamConn so market-data bursts never
// delay a transaction reply.
func (c *Client) PostConn() *ws.Conn {
	c.postOnce.Do(func() {
		c.postConn = ws.NewConn(c.wsConfig(), c.cfg.Logger, c.cfg.Metrics)
		c.postConn.Start(c.lifeCtx)
	})
	return c.postConn
}

// WarmUpStream connects the subscription socket and waits until it is live.
func (c *Client) WarmUpStream(ctx context.Context) error {
	return c.StreamConn().EnsureReady(ctx)
}

// WarmUpPost connects the transaction socket and waits until it is live, so
// the first WebSocket transaction does not pay for the handshake.
func (c *Client) WarmUpPost(ctx context.Context) error {
	return c.PostConn().EnsureReady(ctx)
}

// StreamConnected reports whether the subscription socket is live right now.
func (c *Client) StreamConnected() bool {
	return c.streamConn != nil && c.streamConn.IsConnected()
}

// Close stops the background work (WebSocket connections, metadata refresh)
// and zeroes the signing keys. Safe to call multiple times.
func (c *Client) Close() error {
	c.lifeCancel()
	if c.streamConn != nil {
		c.streamConn.Close()
	}
	if c.postConn != nil {
		c.postConn.Close()
	}
	if c.rest != nil {
		c.rest.Close()
	}
	var i int
	for i = 0; i < len(c.signers); i++ {
		c.signers[i].Close()
	}
	return nil
}
