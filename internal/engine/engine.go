/*
FILE: internal/engine/engine.go

DESCRIPTION:
The unified request layer of the SDK — the single place where a request to
Lighter is assembled, signed, sent and decoded. Sections (perpetuals, spot,
...) NEVER talk to the transports directly and never call each other: every
section method is this layer's function plus the section's own specifics
(market resolution, precision).

    section.Trading().CreateOrder ──► Engine.Send   ──► POST sendTx | WS jsonapi/sendtx
    section.MarketData().GetBook  ──► Engine.Query  ──► GET /api/v1/<endpoint>

SEND PIPELINE (hot path):
  1. key + nonce     : the lane of the signing key hands out the nonce
                       (sequential lanes serialise the round trip, see
                       internal/nonce);
  2. header          : account, api key, expiry, attributes (SkipNonce);
  3. validate        : the reference signer's rules (internal/tx);
  4. hash + sign     : Poseidon2 → Schnorr;
  5. tx_info + form  : allocation-free writers into a pooled buffer;
  6. transport       : REST (default) or WS;
  7. rate accounting : SDK-side limiter + observer event;
  8. decode          : {code, tx_hash, predicted_execution_time_ms} → receipt;
  9. lane outcome    : accepted / rejected / nonce error / unknown.
Steps 1-3, 5 and 7 do not allocate; step 4 allocates inside the Schnorr
implementation (see internal/signing); the HTTP client allocates as any
net/http round trip does.

TRANSPORT SELECTION:
REST is the default. WS is opt-in per call (SendOptions via the section's
Trading().WS()) and is never used as a silent fallback: when the socket is
down the call fails fast, because re-routing or re-sending a transaction
behind the caller's back is unsafe.

DEPENDENCIES:
- internal/tx, signing, nonce, rest, ws, ratelimit, codec, lterr, ltlog.
*/

package engine

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonymontanov/go-lighter/internal/codec"
	"github.com/tonymontanov/go-lighter/internal/lterr"
	"github.com/tonymontanov/go-lighter/internal/ltlog"
	"github.com/tonymontanov/go-lighter/internal/ltmet"
	"github.com/tonymontanov/go-lighter/internal/nonce"
	"github.com/tonymontanov/go-lighter/internal/ratelimit"
	"github.com/tonymontanov/go-lighter/internal/rest"
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/internal/tx"
	"github.com/tonymontanov/go-lighter/internal/ws"
	"github.com/tonymontanov/go-lighter/types"
)

// Transport — how a request travels to the exchange.
type Transport uint8

const (
	// TransportREST — HTTP (default).
	TransportREST Transport = iota
	// TransportWS — WebSocket jsonapi/sendtx.
	TransportWS
)

const (
	// MaxBatchSizeREST — "maximum 50 transactions allowed per batch" (error 21514).
	MaxBatchSizeREST int = 50
	// MaxBatchSizeWS — "up to 15 transactions in a single message" (WebSocket docs).
	MaxBatchSizeWS int = 15
	// MaxAuthTokenLifetime — the exchange accepts deadlines at most 8 hours ahead.
	MaxAuthTokenLifetime time.Duration = 8 * time.Hour
	// authTokenRefreshMargin — regenerate the token this long before expiry.
	authTokenRefreshMargin time.Duration = 30 * time.Minute
)

// Key — one signing key of the client.
type Key struct {
	Index  uint8
	Signer *signing.Signer
}

// Config — engine wiring. Built by the root Client.
type Config struct {
	REST *rest.Client
	// PostConn — lazy accessor of the WS post connection (nil → WS transport unavailable).
	PostConn func() *ws.Conn
	// AccountIndex — account the transactions act on.
	AccountIndex int64
	// Keys — signing keys; Keys[0] is the default. Empty → read-only client.
	Keys []Key
	// ChainID — 304 mainnet, 300 testnet.
	ChainID uint32
	// NonceMode — sequential or skip.
	NonceMode nonce.Mode
	// TxExpiry — lifetime of a transaction (ExpiredAt = now + TxExpiry).
	TxExpiry time.Duration
	// AuthTokenLifetime — lifetime of generated auth tokens (<= 8h).
	AuthTokenLifetime time.Duration
	// PostTimeout — how long a WS post waits for its reply.
	PostTimeout time.Duration
	// Limiter — SDK-side rate-limit accounting.
	Limiter *ratelimit.Limiter
	// RejectWhenRateLimited — fail locally when the limiter has no room.
	RejectWhenRateLimited bool
	Logger                ltlog.Logger
	Metrics               ltmet.CounterFactory
}

// keyLane — a signing key with its nonce lane.
type keyLane struct {
	key  Key
	lane *nonce.Lane
}

// authToken — cached auth token.
type authToken struct {
	token   string
	expires int64 // unix seconds
}

// Engine — unified request layer. Safe for concurrent use.
type Engine struct {
	cfg     Config
	keys    []keyLane
	byIndex [256]*keyLane
	buffers sync.Pool
	auth    atomic.Pointer[authToken]
	authMu  sync.Mutex
	cResync ltmet.Counter
	cSendTx ltmet.Counter
}

// New creates the engine. Lanes are created for every key; no I/O happens
// until the first send.
func New(cfg Config) *Engine {
	if cfg.Logger == nil {
		cfg.Logger = ltlog.Noop()
	}
	if cfg.Metrics == nil {
		cfg.Metrics = ltmet.Noop()
	}
	if cfg.TxExpiry <= 0 {
		cfg.TxExpiry = 10*time.Minute - time.Second
	}
	if cfg.AuthTokenLifetime <= 0 || cfg.AuthTokenLifetime > MaxAuthTokenLifetime {
		cfg.AuthTokenLifetime = 7 * time.Hour
	}
	if cfg.PostTimeout <= 0 {
		cfg.PostTimeout = 10 * time.Second
	}
	var e *Engine = &Engine{cfg: cfg}
	e.buffers.New = func() any {
		var b []byte = make([]byte, 0, 4096)
		return &b
	}
	e.cResync = cfg.Metrics.Counter("lighter_nonce_resync_total")
	e.cSendTx = cfg.Metrics.Counter("lighter_rest_requests_total", "endpoint", "sendTx")
	var i int
	for i = 0; i < len(cfg.Keys); i++ {
		var kl *keyLane = &keyLane{key: cfg.Keys[i]}
		kl.lane = nonce.NewLane(cfg.AccountIndex, cfg.Keys[i].Index, cfg.NonceMode, e.fetchNonce)
		e.keys = append(e.keys, *kl)
		e.byIndex[cfg.Keys[i].Index] = &e.keys[len(e.keys)-1]
	}
	// The slice may have been reallocated during appends: rebuild the index.
	for i = 0; i < len(e.keys); i++ {
		e.byIndex[e.keys[i].key.Index] = &e.keys[i]
	}
	return e
}

// CanSign reports whether at least one signing key is configured.
func (e *Engine) CanSign() bool { return len(e.keys) > 0 && e.keys[0].key.Signer.Enabled() }

// AccountIndex returns the configured account.
func (e *Engine) AccountIndex() int64 { return e.cfg.AccountIndex }

// ChainID returns the configured chain id.
func (e *Engine) ChainID() uint32 { return e.cfg.ChainID }

// Limiter returns the SDK-side limiter.
func (e *Engine) Limiter() *ratelimit.Limiter { return e.cfg.Limiter }

// Logger returns the logger.
func (e *Engine) Logger() ltlog.Logger { return e.cfg.Logger }

// DefaultAPIKeyIndex returns the index of the default signing key (0 when none).
func (e *Engine) DefaultAPIKeyIndex() uint8 {
	if len(e.keys) == 0 {
		return 0
	}
	return e.keys[0].key.Index
}

// APIKeyIndexes returns the indexes of the configured keys.
func (e *Engine) APIKeyIndexes() []uint8 {
	var out []uint8 = make([]uint8, len(e.keys))
	var i int
	for i = 0; i < len(e.keys); i++ {
		out[i] = e.keys[i].key.Index
	}
	return out
}

// PublicKeyHex returns the public key of a configured key ("" when unknown).
func (e *Engine) PublicKeyHex(apiKeyIndex uint8) string {
	var kl *keyLane = e.byIndex[apiKeyIndex]
	if kl == nil {
		return ""
	}
	return kl.key.Signer.PublicKeyHex()
}

// Lane returns the nonce lane of a key (diagnostics / manual resync); nil when unknown.
func (e *Engine) Lane(apiKeyIndex uint8) *nonce.Lane {
	var kl *keyLane = e.byIndex[apiKeyIndex]
	if kl == nil {
		return nil
	}
	return kl.lane
}

// selectKey resolves the key of a send: opts.APIKeyIndex or the default.
func (e *Engine) selectKey(apiKeyIndex uint8) (*keyLane, error) {
	if len(e.keys) == 0 || !e.keys[0].key.Signer.Enabled() {
		return nil, lterr.New(lterr.ErrorKindAuth, "engine: no signing key is configured", signing.ErrSignerDisabled)
	}
	if apiKeyIndex == 0 {
		return &e.keys[0], nil
	}
	var kl *keyLane = e.byIndex[apiKeyIndex]
	if kl == nil {
		return nil, lterr.New(lterr.ErrorKindInvalidRequest, "engine: api key index is not configured", nil)
	}
	return kl, nil
}

// nextNonceResponse — nextNonce endpoint answer.
type nextNonceResponse struct {
	Nonce int64 `json:"nonce"`
}

// fetchNonce is the lane fetcher: GET nextNonce.
func (e *Engine) fetchNonce(ctx context.Context, accountIndex int64, apiKeyIndex uint8) (int64, error) {
	var q rest.Query
	q.Int("account_index", accountIndex).Int("api_key_index", int64(apiKeyIndex))
	var out nextNonceResponse
	var err error = e.Query(ctx, ratelimit.EndpointNextNonce, q.String(), false, &out, ratelimit.CategoryOther)
	if err != nil {
		return 0, err
	}
	e.cResync.Inc()
	return out.Nonce, nil
}

// NextNonce fetches the next nonce of a key from the exchange (diagnostics;
// the lanes call it themselves when they need to resync).
func (e *Engine) NextNonce(ctx context.Context, apiKeyIndex uint8) (int64, error) {
	return e.fetchNonce(ctx, e.cfg.AccountIndex, apiKeyIndex)
}

/*
AuthToken returns a cached authentication token of the default key,
regenerating it when it is within authTokenRefreshMargin of its expiry.
Tokens are bound to the key: rotating the key invalidates them.
*/
func (e *Engine) AuthToken() (string, error) {
	var now int64 = time.Now().Unix()
	var current *authToken = e.auth.Load()
	if current != nil && current.expires-now > int64(authTokenRefreshMargin/time.Second) {
		return current.token, nil
	}
	e.authMu.Lock()
	defer e.authMu.Unlock()
	current = e.auth.Load()
	if current != nil && current.expires-now > int64(authTokenRefreshMargin/time.Second) {
		return current.token, nil
	}
	if !e.CanSign() {
		return "", lterr.New(lterr.ErrorKindAuth, "engine: auth token needs a signing key", signing.ErrSignerDisabled)
	}
	var deadline int64 = now + int64(e.cfg.AuthTokenLifetime/time.Second)
	var token string
	var err error
	token, err = signing.AuthToken(e.keys[0].key.Signer, deadline, e.cfg.AccountIndex, e.keys[0].key.Index)
	if err != nil {
		return "", lterr.New(lterr.ErrorKindAuth, "engine: auth token", err)
	}
	e.auth.Store(&authToken{token: token, expires: deadline})
	return token, nil
}

// admitREST applies the optional local guard of a read.
func (e *Engine) admitREST(endpoint string) error {
	if !e.cfg.RejectWhenRateLimited || e.cfg.Limiter == nil {
		return nil
	}
	if !e.cfg.Limiter.AdmitREST(endpoint) {
		return lterr.New(lterr.ErrorKindRateLimit, "engine: SDK-side rate limit reached for "+endpoint, nil)
	}
	return nil
}

// admitSendTx applies the optional local guard of a send.
func (e *Engine) admitSendTx(txType types.TxType, count int) error {
	if !e.cfg.RejectWhenRateLimited || e.cfg.Limiter == nil {
		return nil
	}
	if !e.cfg.Limiter.AdmitSendTx(txType, count) {
		return lterr.New(lterr.ErrorKindRateLimit, "engine: SDK-side rate limit reached for sendTx", nil)
	}
	return nil
}

// statusOf extracts the HTTP status of a transport error (200 when err is nil).
func statusOf(err error) int {
	if err == nil {
		return 200
	}
	var e *lterr.Error
	if errors.As(err, &e) {
		return e.HTTPStatus
	}
	return 0
}

/*
Query performs a GET request. endpoint is the path without /api/v1/ (used
for weights and diagnostics); query is the encoded query string; private
attaches the auth token; dest receives the decoded response (nil to skip).
*/
func (e *Engine) Query(ctx context.Context, endpoint string, query string, private bool, dest any, category string) error {
	var err error = e.admitREST(endpoint)
	if err != nil {
		return err
	}
	var auth string
	if private {
		auth, err = e.AuthToken()
		if err != nil {
			return err
		}
	}
	var result rest.Result
	result, err = e.cfg.REST.Get(ctx, endpoint, query, auth)
	if e.cfg.Limiter != nil {
		e.cfg.Limiter.AccountREST(endpoint, statusOf(err), category)
	}
	if err != nil {
		return err
	}
	if dest != nil {
		err = codec.Unmarshal(result.Body, dest)
		if err != nil {
			return lterr.New(lterr.ErrorKindUnknown, "engine: "+endpoint+": parse response", err)
		}
	}
	return nil
}

// PostJSON performs a POST with a JSON body (management endpoints).
func (e *Engine) PostJSON(ctx context.Context, endpoint string, body []byte, private bool, dest any, category string) error {
	var err error = e.admitREST(endpoint)
	if err != nil {
		return err
	}
	var auth string
	if private {
		auth, err = e.AuthToken()
		if err != nil {
			return err
		}
	}
	var result rest.Result
	result, err = e.cfg.REST.PostJSON(ctx, endpoint, body, auth)
	if e.cfg.Limiter != nil {
		e.cfg.Limiter.AccountREST(endpoint, statusOf(err), category)
	}
	if err != nil {
		return err
	}
	if dest != nil {
		err = codec.Unmarshal(result.Body, dest)
		if err != nil {
			return lterr.New(lterr.ErrorKindUnknown, "engine: "+endpoint+": parse response", err)
		}
	}
	return nil
}

// SendOptions — per-call options of a send (mirror of types.SendOptions plus transport).
type SendOptions struct {
	Transport Transport
	// APIKeyIndex — signing key; 0 = default.
	APIKeyIndex uint8
	// Nonce — explicit nonce (bypasses the lane); 0 = lane.
	Nonce int64
	// ExpiredAtMs — explicit expiry; 0 = now + TxExpiry.
	ExpiredAtMs int64
	// SkipNonce — force the SkipNonce attribute.
	SkipNonce bool
	// PriceProtection — sendTx price_protection form field (REST).
	PriceProtection bool
	// Category — rate-limit category of the send.
	Category string
}

// prepared — a transaction ready to go on the wire.
type prepared struct {
	hash signing.Hash
	sig  signing.Signature
}

// fill completes the header of a transaction and validates it.
func (e *Engine) fill(t tx.Tx, kl *keyLane, nonceValue int64, opts *SendOptions) error {
	var h *tx.Header = t.Head()
	h.AccountIndex = e.cfg.AccountIndex
	h.APIKeyIndex = kl.key.Index
	h.Nonce = nonceValue
	if opts.ExpiredAtMs != 0 {
		h.ExpiredAt = opts.ExpiredAtMs
	} else {
		h.ExpiredAt = time.Now().Add(e.cfg.TxExpiry).UnixMilli()
	}
	if opts.SkipNonce || (opts.Nonce == 0 && kl.lane.Mode() == nonce.ModeSkip) {
		h.Attributes.SkipNonce = true
	}
	var err error = t.Validate()
	if err != nil {
		return lterr.New(lterr.ErrorKindInvalidRequest, "engine: "+t.Type().String()+": "+err.Error(), err)
	}
	return nil
}

// sign hashes and signs a filled transaction.
func (e *Engine) sign(t tx.Tx, kl *keyLane, p *prepared) error {
	t.Hash(e.cfg.ChainID, &p.hash)
	var err error = kl.key.Signer.Sign(&p.hash, &p.sig)
	if err != nil {
		return lterr.New(lterr.ErrorKindAuth, "engine: sign "+t.Type().String(), err)
	}
	return nil
}

// sendTxResponse — sendTx / sendTxBatch answer (tx_hash is a string or an array).
type sendTxResponse struct {
	Code                     int              `json:"code"`
	Message                  string           `json:"message"`
	TxHash                   codec.RawMessage `json:"tx_hash"`
	PredictedExecutionTimeMs int64            `json:"predicted_execution_time_ms"`
	VolumeQuotaRemaining     int64            `json:"volume_quota_remaining"`
}

// outcomeOf maps a send error to the lane outcome.
func outcomeOf(err error) nonce.Outcome {
	if err == nil {
		return nonce.OutcomeAccepted
	}
	if lterr.IsNonceError(err) {
		return nonce.OutcomeNonceError
	}
	var e *lterr.Error
	if errors.As(err, &e) {
		if e.Code != 0 || e.HTTPStatus == 429 || e.HTTPStatus == 405 || e.Kind == lterr.ErrorKindInvalidRequest {
			// The API server answered: the transaction was not ingested.
			return nonce.OutcomeRejected
		}
	}
	return nonce.OutcomeUnknown
}

/*
Send signs and sends one transaction and returns the API-level receipt.

Errors:
  - *lterr.Error with Kind InvalidRequest (local validation), Auth (no key,
    signature / key problems), RateLimit, Network;
  - *lterr.Error built from the exchange envelope when the API server
    rejected the transaction (the nonce is then reused);
  - sequencer-level rejections are NOT errors of the call (see types.TxOutcome).
*/
func (e *Engine) Send(ctx context.Context, t tx.Tx, opts SendOptions) (types.TxReceipt, error) {
	var receipt types.TxReceipt
	var kl *keyLane
	var err error
	kl, err = e.selectKey(opts.APIKeyIndex)
	if err != nil {
		return receipt, err
	}
	err = e.admitSendTx(t.Type(), 1)
	if err != nil {
		return receipt, err
	}
	if opts.Category == "" {
		opts.Category = ratelimit.CategoryOther
	}

	var reservation nonce.Reservation
	var useLane bool = opts.Nonce == 0
	if useLane && kl.lane.Serialized() {
		kl.lane.Lock()
		defer kl.lane.Unlock()
	}
	if useLane {
		reservation, err = kl.lane.Acquire(ctx, 1)
		if err != nil {
			return receipt, err
		}
	} else {
		reservation = nonce.Reservation{Nonce: opts.Nonce, Count: 1}
	}

	err = e.fill(t, kl, reservation.Nonce, &opts)
	if err != nil {
		if useLane {
			kl.lane.Release(reservation, nonce.OutcomeRejected)
		}
		return receipt, err
	}
	var p prepared
	err = e.sign(t, kl, &p)
	if err != nil {
		if useLane {
			kl.lane.Release(reservation, nonce.OutcomeRejected)
		}
		return receipt, err
	}
	receipt.TxType = t.Type()
	receipt.Nonce = reservation.Nonce
	receipt.APIKeyIndex = kl.key.Index

	var pooled *[]byte = e.buffers.Get().(*[]byte)
	var info []byte = (*pooled)[:0]
	info = t.AppendInfo(info, &p.sig)

	var response sendTxResponse
	if opts.Transport == TransportWS {
		var data []byte = make([]byte, 0, len(info)+32)
		data = append(data, `"tx_type":`...)
		data = appendUint(data, uint64(t.Type()))
		data = append(data, `,"tx_info":`...)
		data = append(data, info...)
		err = e.postWS(ctx, ws.PostTypeSendTx, data, &response)
		if e.cfg.Limiter != nil {
			e.cfg.Limiter.AccountSendTx(ratelimit.EndpointSendTx, ratelimit.TransportWS, 0, t.Type(), 1, opts.Category)
		}
	} else {
		var form []byte = e.formBuffer(len(info))
		form = rest.AppendSendTxForm(form[:0], uint8(t.Type()), info, opts.PriceProtection)
		var result rest.Result
		result, err = e.cfg.REST.PostForm(ctx, ratelimit.EndpointSendTx, form, "")
		e.cSendTx.Inc()
		if e.cfg.Limiter != nil {
			e.cfg.Limiter.AccountSendTx(ratelimit.EndpointSendTx, ratelimit.TransportREST, statusOf(err), t.Type(), 1, opts.Category)
		}
		if err == nil {
			err = decodeReceipt(result.Body, &response)
		}
		e.putForm(form)
	}
	*pooled = info
	e.buffers.Put(pooled)

	if useLane {
		kl.lane.Release(reservation, outcomeOf(err))
	}
	if err != nil {
		return receipt, err
	}
	receipt.TxHash = hashString(response.TxHash)
	receipt.PredictedExecutionTimeMs = response.PredictedExecutionTimeMs
	receipt.VolumeQuotaRemaining = response.VolumeQuotaRemaining
	if receipt.TxHash == "" {
		receipt.TxHash = p.hash.Hex()
	}
	return receipt, nil
}

/*
SendBatch signs and sends several transactions in one sendTxBatch. All
transactions use the same key and consecutive nonces (an exchange rule:
"all transactions in the batch must use the same account and api key",
"batch transaction nonce is not increasing").
*/
func (e *Engine) SendBatch(ctx context.Context, txs []tx.Tx, opts SendOptions) (types.BatchReceipt, error) {
	var receipt types.BatchReceipt
	if len(txs) == 0 {
		return receipt, lterr.New(lterr.ErrorKindInvalidRequest, "engine: empty batch", nil)
	}
	var maxSize int = MaxBatchSizeREST
	if opts.Transport == TransportWS {
		maxSize = MaxBatchSizeWS
	}
	if len(txs) > maxSize {
		return receipt, lterr.New(lterr.ErrorKindInvalidRequest, "engine: batch exceeds the maximum size", nil)
	}
	var kl *keyLane
	var err error
	kl, err = e.selectKey(opts.APIKeyIndex)
	if err != nil {
		return receipt, err
	}
	err = e.admitSendTx(txs[0].Type(), len(txs))
	if err != nil {
		return receipt, err
	}
	if opts.Category == "" {
		opts.Category = ratelimit.CategoryOther
	}

	var reservation nonce.Reservation
	var useLane bool = opts.Nonce == 0
	if useLane && kl.lane.Serialized() {
		kl.lane.Lock()
		defer kl.lane.Unlock()
	}
	if useLane {
		reservation, err = kl.lane.Acquire(ctx, len(txs))
		if err != nil {
			return receipt, err
		}
	} else {
		reservation = nonce.Reservation{Nonce: opts.Nonce, Count: len(txs)}
	}
	receipt.FirstNonce = reservation.Nonce
	receipt.Count = len(txs)
	receipt.APIKeyIndex = kl.key.Index

	var pooled *[]byte = e.buffers.Get().(*[]byte)
	var scratch []byte = (*pooled)[:0]
	var infos [][]byte = make([][]byte, len(txs))
	var txTypes []uint8 = make([]uint8, len(txs))
	var hashes []signing.Hash = make([]signing.Hash, len(txs))
	var i int
	for i = 0; i < len(txs); i++ {
		err = e.fill(txs[i], kl, reservation.Nonce+int64(i), &opts)
		if err != nil {
			break
		}
		var p prepared
		err = e.sign(txs[i], kl, &p)
		if err != nil {
			break
		}
		hashes[i] = p.hash
		txTypes[i] = uint8(txs[i].Type())
		var start int = len(scratch)
		scratch = txs[i].AppendInfo(scratch, &p.sig)
		infos[i] = scratch[start:len(scratch):len(scratch)]
	}
	if err != nil {
		*pooled = scratch
		e.buffers.Put(pooled)
		if useLane {
			kl.lane.Release(reservation, nonce.OutcomeRejected)
		}
		return receipt, err
	}
	// scratch may have been reallocated while appending: re-slice the infos.
	var offset int
	for i = 0; i < len(txs); i++ {
		var n int = len(infos[i])
		infos[i] = scratch[offset : offset+n : offset+n]
		offset += n
	}

	var response sendTxResponse
	if opts.Transport == TransportWS {
		var data []byte = make([]byte, 0, len(scratch)+64)
		data = append(data, `"tx_types":"`...)
		data = rest.AppendJSONTxTypes(data, txTypes)
		data = append(data, `","tx_infos":`...)
		var quoted []byte = rest.AppendJSONTxInfos(nil, infos)
		data = appendJSONString(data, quoted)
		err = e.postWS(ctx, ws.PostTypeSendTxBatch, data, &response)
		if e.cfg.Limiter != nil {
			e.cfg.Limiter.AccountSendTx(ratelimit.EndpointSendTxBatch, ratelimit.TransportWS, 0, txs[0].Type(), len(txs), opts.Category)
		}
	} else {
		var form []byte = e.formBuffer(len(scratch) * 3)
		var tmp []byte
		form, tmp = rest.AppendSendTxBatchForm(form[:0], nil, txTypes, infos)
		_ = tmp
		var result rest.Result
		result, err = e.cfg.REST.PostForm(ctx, ratelimit.EndpointSendTxBatch, form, "")
		e.cSendTx.Inc()
		if e.cfg.Limiter != nil {
			e.cfg.Limiter.AccountSendTx(ratelimit.EndpointSendTxBatch, ratelimit.TransportREST, statusOf(err), txs[0].Type(), len(txs), opts.Category)
		}
		if err == nil {
			err = decodeReceipt(result.Body, &response)
		}
		e.putForm(form)
	}
	*pooled = scratch
	e.buffers.Put(pooled)

	if useLane {
		kl.lane.Release(reservation, outcomeOf(err))
	}
	if err != nil {
		return receipt, err
	}
	receipt.TxHashes = hashList(response.TxHash)
	if len(receipt.TxHashes) == 0 {
		receipt.TxHashes = make([]string, len(txs))
		for i = 0; i < len(txs); i++ {
			receipt.TxHashes[i] = hashes[i].Hex()
		}
	}
	receipt.PredictedExecutionTimeMs = response.PredictedExecutionTimeMs
	receipt.VolumeQuotaRemaining = response.VolumeQuotaRemaining
	return receipt, nil
}

// formPool — buffers of form bodies (separate from tx_info buffers because
// both are alive at the same time).
var formPool sync.Pool = sync.Pool{New: func() any {
	var b []byte = make([]byte, 0, 8192)
	return &b
}}

// formBuffer takes a form buffer with at least the given capacity hint.
func (e *Engine) formBuffer(hint int) []byte {
	var pooled *[]byte = formPool.Get().(*[]byte)
	var b []byte = *pooled
	if cap(b) < hint {
		b = make([]byte, 0, hint+256)
	}
	return b
}

// putForm returns a form buffer to the pool.
func (e *Engine) putForm(b []byte) {
	var pooled *[]byte = &b
	formPool.Put(pooled)
}

// decodeReceipt decodes a sendTx / sendTxBatch answer.
func decodeReceipt(body []byte, out *sendTxResponse) error {
	var err error = codec.Unmarshal(body, out)
	if err != nil {
		return lterr.New(lterr.ErrorKindUnknown, "engine: sendTx: parse response", err)
	}
	if out.Code != 0 && out.Code != lterr.CodeOK {
		return lterr.FromExchange(200, out.Code, out.Message)
	}
	return nil
}

// hashString decodes a JSON string tx_hash ("" when absent or not a string).
func hashString(raw codec.RawMessage) string {
	if len(raw) < 2 || raw[0] != '"' {
		return ""
	}
	var s string
	if codec.Unmarshal(raw, &s) != nil {
		return ""
	}
	return s
}

// hashList decodes a JSON array tx_hash (nil when absent or not an array).
func hashList(raw codec.RawMessage) []string {
	if len(raw) < 2 || raw[0] != '[' {
		return nil
	}
	var list []string
	if codec.Unmarshal(raw, &list) != nil {
		return nil
	}
	return list
}

// appendUint appends a decimal unsigned integer.
func appendUint(b []byte, v uint64) []byte {
	if v == 0 {
		return append(b, '0')
	}
	var digits [20]byte
	var i int = len(digits)
	for v > 0 {
		i--
		digits[i] = byte('0' + v%10)
		v /= 10
	}
	return append(b, digits[i:]...)
}

// appendJSONString appends s as a JSON string literal.
func appendJSONString(b []byte, s []byte) []byte {
	b = append(b, '"')
	var i int
	for i = 0; i < len(s); i++ {
		if s[i] == '"' || s[i] == '\\' {
			b = append(b, '\\')
		}
		b = append(b, s[i])
	}
	return append(b, '"')
}

/*
postWS sends a jsonapi request over the post connection and decodes the reply.

The reply shape is not documented beyond "id is returned in the response";
the decoder looks for the envelope fields at the top level and under "data"
and treats {"error":{"code","message"}} as a rejection.
*/
func (e *Engine) postWS(ctx context.Context, msgType string, data []byte, out *sendTxResponse) error {
	if e.cfg.PostConn == nil {
		return lterr.New(lterr.ErrorKindInvalidRequest, "engine: ws transport is not configured", nil)
	}
	var conn *ws.Conn = e.cfg.PostConn()
	var reply ws.PostResult
	var err error
	reply, err = conn.Post(ctx, msgType, data, e.cfg.PostTimeout)
	if err != nil {
		return lterr.New(lterr.ErrorKindNetwork, "engine: ws post "+msgType, err)
	}
	var code int = int(codec.GetInt(reply.Frame, "error", "code"))
	if code != 0 {
		return lterr.FromExchange(0, code, codec.GetString(reply.Frame, "error", "message"))
	}
	var payload []byte = reply.Frame
	if codec.GetInt(reply.Frame, "data", "code") != 0 || codec.GetString(reply.Frame, "data", "tx_hash") != "" {
		var wrapped struct {
			Data codec.RawMessage `json:"data"`
		}
		if codec.Unmarshal(reply.Frame, &wrapped) == nil && len(wrapped.Data) > 0 {
			payload = wrapped.Data
		}
	}
	return decodeReceipt(payload, out)
}
