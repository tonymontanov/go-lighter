/*
FILE: config.go

DESCRIPTION:
config.go defines the SDK configuration structs together with the mainnet /
testnet endpoints and default values.

MAIN FUNCTIONS:
  - DefaultConfig(): returns Config with mainnet endpoints and default values
    for timeouts / reconnect / keepalive.
  - (Config).withDefaults(): fills empty Config fields with defaults. Inside
    the SDK the config is ALWAYS passed through withDefaults() first; the
    caller's struct is never mutated.

ENDPOINTS (official "Get Started" page):
  mainnet : https://mainnet.zklighter.elliot.ai   wss://mainnet.zklighter.elliot.ai/stream   chain id 304
  testnet : https://testnet.zklighter.elliot.ai   wss://testnet.zklighter.elliot.ai/stream   chain id 300
Config.Testnet selects the pair AND the chain id signed into every
transaction — a mainnet signature is rejected by the testnet and vice versa.
Explicitly set URLs are never overridden (mock servers, proxies).

AUTHENTICATION (API key model — there is no wallet key in the SDK):
  - AccountIndex : the account (or sub-account) the client trades. Found
                   with accountsByL1Address or the account endpoint.
  - APIKeyIndex  : index of the API key (2..254; 0..1 are reserved for the
                   web and mobile clients) whose private key is PrivateKey.
  - PrivateKey   : 40-byte hex private key of that API key, registered on the
                   account with a changePubKey transaction. Empty → the client
                   can only access public endpoints.
  - ExtraKeys    : additional API keys of the same account (v2.0 key pool:
                   independent nonce lanes, selectable per call).

DEPENDENCIES:
Standard library:
  - time: timeouts and reconnect/keepalive intervals.
  - net/http, net/url: optional proxy / custom HTTP client injection.
*/

package lighter

import (
	"net/http"
	"net/url"
	"time"

	"github.com/tonymontanov/go-lighter/internal/nonce"
)

// Lighter endpoints. Declared as vars rather than const so tests can override
// them (e.g. to point at a mock server).
var (
	// MainnetRestURL — production REST base URL (scheme + host).
	MainnetRestURL string = "https://mainnet.zklighter.elliot.ai"
	// MainnetWsURL — production WebSocket URL.
	MainnetWsURL string = "wss://mainnet.zklighter.elliot.ai/stream"
	// TestnetRestURL — testnet REST base URL.
	TestnetRestURL string = "https://testnet.zklighter.elliot.ai"
	// TestnetWsURL — testnet WebSocket URL.
	TestnetWsURL string = "wss://testnet.zklighter.elliot.ai/stream"
)

// Chain ids signed into every transaction.
const (
	// MainnetChainID — 304.
	MainnetChainID uint32 = 304
	// TestnetChainID — 300.
	TestnetChainID uint32 = 300
)

// NonceMode — nonce assignment strategy (see internal/nonce).
type NonceMode = nonce.Mode

const (
	// NonceModeSequential — old+1 nonces, one round trip at a time per key
	// (default; the strategy of the official SDKs).
	NonceModeSequential NonceMode = nonce.ModeSequential
	// NonceModeSkip — monotonic millisecond nonces with the SkipNonce
	// attribute; sends on one key need not be serialised.
	NonceModeSkip NonceMode = nonce.ModeSkip
)

// APIKeyConfig — one additional API key of the account.
type APIKeyConfig struct {
	// Index — API key index (2..254).
	Index uint8
	// PrivateKey — 40-byte hex private key, optional 0x prefix.
	PrivateKey string
}

// Config — public SDK configuration. Passed to NewClient.
type Config struct {
	// AccountIndex — account (or sub-account) index. Required for private
	// endpoints and transactions.
	AccountIndex int64
	// APIKeyIndex — index of the default signing key (2..254).
	APIKeyIndex uint8
	// PrivateKey — private key of the default signing key. Empty → read-only
	// client. Never logged, never echoed in errors.
	PrivateKey string
	// ExtraKeys — additional signing keys (v2.0 key pool).
	ExtraKeys []APIKeyConfig

	// Testnet — selects testnet endpoints and the testnet chain id.
	Testnet bool
	// ChainID — explicit chain id; 0 → derived from Testnet.
	ChainID uint32

	// REST — REST transport settings. Empty fields take DefaultConfig().REST.
	REST RestConfig
	// WS — WebSocket transport settings. Empty fields take DefaultConfig().WS.
	WS WsConfig
	// RateLimit — SDK-side rate-limit accounting.
	RateLimit RateLimitConfig

	// NonceMode — sequential (default) or skip.
	NonceMode NonceMode
	// TxExpiry — lifetime of a transaction (ExpiredAt = now + TxExpiry).
	// Default: 10 minutes minus one second (the official SDK default).
	TxExpiry time.Duration
	// AuthTokenLifetime — validity of generated auth tokens, at most 8 hours
	// (exchange limit). Default: 7h.
	AuthTokenLifetime time.Duration

	// MarketRefreshInterval — how often sections refresh market metadata
	// (market ids, decimals). Default: 5m. Negative disables the background
	// refresh (metadata is then loaded once, on first use).
	MarketRefreshInterval time.Duration

	// Logger — optional logger. If nil, NoopLogger() is used.
	Logger Logger
	// Metrics — optional counter factory. If nil, NoopMetrics() is used.
	Metrics CounterFactory
	// UserAgent — User-Agent of REST requests. Default: "go-lighter/v1".
	UserAgent string

	// RateLimitEventObserver — optional hook called SYNCHRONOUSLY after every
	// request with the SDK-side rate-limit accounting (see rate-limit-event.go
	// for the contract). If nil — no-op, zero overhead.
	RateLimitEventObserver func(RateLimitEvent)
}

// RestConfig — HTTP transport settings.
type RestConfig struct {
	// BaseURL — REST base URL (scheme + host). Default: MainnetRestURL
	// (TestnetRestURL when Config.Testnet).
	BaseURL string
	// RequestTimeout — timeout of a single REST request. Default: 10s. For
	// latency-critical calls pass a ctx with its own deadline.
	RequestTimeout time.Duration
	// MaxIdleConns — idle connection pool size. Default: 100.
	MaxIdleConns int
	// MaxIdleConnsPerHost — pool size per host. Default: 100.
	MaxIdleConnsPerHost int
	// IdleConnTimeout — keep-alive idle timeout. Default: 90s.
	IdleConnTimeout time.Duration
	// Proxy — optional proxy selector. Default: http.ProxyFromEnvironment.
	Proxy func(*http.Request) (*url.URL, error)
	// HTTPClient — optional fully custom *http.Client (own TLS config, dialer,
	// transport). When set, the pool / proxy / timeout fields are ignored.
	HTTPClient *http.Client
}

// WsConfig — WebSocket transport settings.
type WsConfig struct {
	// URL — WebSocket URL. Default: MainnetWsURL (TestnetWsURL when Testnet).
	URL string
	// HandshakeTimeout — connection handshake timeout. Default: 10s.
	HandshakeTimeout time.Duration
	// ReadTimeout — read deadline of a single frame; refreshed by every frame
	// including pong. Must exceed PingInterval. Default: 75s.
	ReadTimeout time.Duration
	// WriteTimeout — write timeout of a single frame. Default: 5s.
	WriteTimeout time.Duration
	// PingInterval — client keepalive period. The server closes a connection
	// silent for 2 minutes. Default: 30s.
	PingInterval time.Duration
	// ReconnectInitialBackoff — initial delay between reconnects. Default: 200ms.
	ReconnectInitialBackoff time.Duration
	// ReconnectMaxBackoff — upper bound of the backoff. Default: 10s.
	ReconnectMaxBackoff time.Duration
	// ReconnectJitter — relative jitter [0..1] applied to the backoff. Default: 0.2.
	ReconnectJitter float64
	// ReadBufferSize — gorilla/websocket read buffer size. Default: 64KB.
	ReadBufferSize int
	// WriteBufferSize — gorilla/websocket write buffer size. Default: 16KB.
	WriteBufferSize int
	// EnableCompression — negotiate permessage-deflate. Default: false.
	EnableCompression bool
	// PostTimeout — how long a WS jsonapi request waits for its reply. Default: 10s.
	PostTimeout time.Duration
	// Proxy — optional proxy selector. Default: http.ProxyFromEnvironment.
	Proxy func(*http.Request) (*url.URL, error)
}

// RateLimitConfig — SDK-side rate-limit accounting (see rate-limit-event.go).
type RateLimitConfig struct {
	// Tier — "standard" (default), "premium", "plus" or "builder".
	Tier string
	// StakedLIT — whole LIT tokens staked; sizes the Premium sendTx bucket.
	StakedLIT int64
	// DefaultTxTypeLimit — per-minute limit applied to transaction types
	// without a documented bucket (order transactions); 0 disables.
	DefaultTxTypeLimit int64
	// RejectWhenRateLimited — when true, a request that does not fit into the
	// SDK-side budgets (or arrives during a cooldown) fails locally with
	// ErrorKindRateLimit instead of being sent. Default: false (the desk's
	// own rate limiter decides).
	RejectWhenRateLimited bool
}

// DefaultConfig returns a Config with all sensible defaults (mainnet
// endpoints + production timeouts).
func DefaultConfig() Config {
	return Config{
		REST: RestConfig{
			BaseURL:             MainnetRestURL,
			RequestTimeout:      10 * time.Second,
			MaxIdleConns:        100,
			MaxIdleConnsPerHost: 100,
			IdleConnTimeout:     90 * time.Second,
		},
		WS: WsConfig{
			URL:                     MainnetWsURL,
			HandshakeTimeout:        10 * time.Second,
			ReadTimeout:             75 * time.Second,
			WriteTimeout:            5 * time.Second,
			PingInterval:            30 * time.Second,
			ReconnectInitialBackoff: 200 * time.Millisecond,
			ReconnectMaxBackoff:     10 * time.Second,
			ReconnectJitter:         0.2,
			ReadBufferSize:          64 * 1024,
			WriteBufferSize:         16 * 1024,
			PostTimeout:             10 * time.Second,
		},
		TxExpiry:              10*time.Minute - time.Second,
		AuthTokenLifetime:     7 * time.Hour,
		MarketRefreshInterval: 5 * time.Minute,
		Logger:                NoopLogger(),
		Metrics:               NoopMetrics(),
		UserAgent:             "go-lighter/v1",
	}
}

// withDefaults returns a Config where all empty fields are filled with values
// from DefaultConfig(). Used inside NewClient — the user-supplied Config is
// never mutated.
func (c Config) withDefaults() Config {
	var def Config = DefaultConfig()

	// Endpoints: for Testnet use the testnet hosts by default. If the user
	// explicitly set a URL — do NOT override it. DefaultConfig() pre-fills
	// the MAINNET URLs, so the mainnet default counts as "not set" when
	// Testnet is on; any other URL is kept.
	var defRest string = def.REST.BaseURL
	var defWs string = def.WS.URL
	if c.Testnet {
		defRest = TestnetRestURL
		defWs = TestnetWsURL
	}
	if c.REST.BaseURL == "" || (c.Testnet && c.REST.BaseURL == MainnetRestURL) {
		c.REST.BaseURL = defRest
	}
	if c.REST.RequestTimeout == 0 {
		c.REST.RequestTimeout = def.REST.RequestTimeout
	}
	if c.REST.MaxIdleConns == 0 {
		c.REST.MaxIdleConns = def.REST.MaxIdleConns
	}
	if c.REST.MaxIdleConnsPerHost == 0 {
		c.REST.MaxIdleConnsPerHost = def.REST.MaxIdleConnsPerHost
	}
	if c.REST.IdleConnTimeout == 0 {
		c.REST.IdleConnTimeout = def.REST.IdleConnTimeout
	}

	if c.WS.URL == "" || (c.Testnet && c.WS.URL == MainnetWsURL) {
		c.WS.URL = defWs
	}
	if c.WS.HandshakeTimeout == 0 {
		c.WS.HandshakeTimeout = def.WS.HandshakeTimeout
	}
	if c.WS.ReadTimeout == 0 {
		c.WS.ReadTimeout = def.WS.ReadTimeout
	}
	if c.WS.WriteTimeout == 0 {
		c.WS.WriteTimeout = def.WS.WriteTimeout
	}
	if c.WS.PingInterval == 0 {
		c.WS.PingInterval = def.WS.PingInterval
	}
	if c.WS.ReconnectInitialBackoff == 0 {
		c.WS.ReconnectInitialBackoff = def.WS.ReconnectInitialBackoff
	}
	if c.WS.ReconnectMaxBackoff == 0 {
		c.WS.ReconnectMaxBackoff = def.WS.ReconnectMaxBackoff
	}
	if c.WS.ReconnectJitter == 0 {
		c.WS.ReconnectJitter = def.WS.ReconnectJitter
	}
	if c.WS.ReadBufferSize == 0 {
		c.WS.ReadBufferSize = def.WS.ReadBufferSize
	}
	if c.WS.WriteBufferSize == 0 {
		c.WS.WriteBufferSize = def.WS.WriteBufferSize
	}
	if c.WS.PostTimeout == 0 {
		c.WS.PostTimeout = def.WS.PostTimeout
	}

	if c.ChainID == 0 {
		c.ChainID = MainnetChainID
		if c.Testnet {
			c.ChainID = TestnetChainID
		}
	}
	if c.TxExpiry == 0 {
		c.TxExpiry = def.TxExpiry
	}
	if c.AuthTokenLifetime == 0 {
		c.AuthTokenLifetime = def.AuthTokenLifetime
	}
	if c.MarketRefreshInterval == 0 {
		c.MarketRefreshInterval = def.MarketRefreshInterval
	}
	if c.Logger == nil {
		c.Logger = NoopLogger()
	}
	if c.Metrics == nil {
		c.Metrics = NoopMetrics()
	}
	if c.UserAgent == "" {
		c.UserAgent = def.UserAgent
	}
	return c
}

// validate checks the transport and identity fields. Key material is parsed
// in NewClient.
func (c Config) validate() error {
	if c.REST.BaseURL == "" {
		return NewError(ErrorKindInvalidRequest, "config: REST.BaseURL is empty", nil)
	}
	if c.WS.URL == "" {
		return NewError(ErrorKindInvalidRequest, "config: WS.URL is empty", nil)
	}
	if c.WS.ReadTimeout <= c.WS.PingInterval {
		return NewError(ErrorKindInvalidRequest, "config: WS.ReadTimeout must exceed WS.PingInterval", nil)
	}
	if c.AccountIndex < 0 {
		return NewError(ErrorKindInvalidRequest, "config: AccountIndex is negative", nil)
	}
	if c.PrivateKey != "" && c.APIKeyIndex == 255 {
		return NewError(ErrorKindInvalidRequest, "config: APIKeyIndex 255 is reserved", nil)
	}
	if c.AuthTokenLifetime > 8*time.Hour {
		return NewError(ErrorKindInvalidRequest, "config: AuthTokenLifetime exceeds the 8 hour maximum", nil)
	}
	var i int
	for i = 0; i < len(c.ExtraKeys); i++ {
		if c.ExtraKeys[i].Index == c.APIKeyIndex || c.ExtraKeys[i].Index == 255 {
			return NewError(ErrorKindInvalidRequest, "config: ExtraKeys contains a duplicate or reserved index", nil)
		}
	}
	return nil
}
