/*
FILE: internal/rest/client.go

DESCRIPTION:
HTTP transport of the SDK. Lighter's REST API lives under /api/v1/:
  - reads are GET with query parameters, optionally authenticated with an
    auth token in the "authorization" header (private data; also switches
    the rate limit from per-IP to per-L1-address);
  - sendTx / sendTxBatch are POST application/x-www-form-urlencoded;
  - a few management endpoints are POST application/json (v2.5).
Every answer is a JSON envelope {"code": N, "message": "..."} plus the
payload; code 200 means success. Non-200 HTTP statuses carry the same
envelope (400 with an application code, 429 / 405 on rate limits).

This package knows nothing about transactions, signing or sections — it
moves bytes, classifies failures and reports the HTTP status for rate-limit
accounting done one level up (internal/engine).

ERROR STRATEGY:
  - ctx cancellation / deadline, dial and read errors → ErrorKindNetwork;
  - HTTP status != 200 with an envelope             → lterr.FromExchange;
  - HTTP status != 200 without one                  → lterr.MapHTTPStatus;
  - HTTP 200 with envelope code != 200              → lterr.FromExchange.

SECURITY NOTES:
Bodies of sendTx contain signatures and the header carries auth tokens; the
transport never logs either.

DEPENDENCIES:
- internal/codec, internal/lterr, internal/ltlog.
*/

package rest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tonymontanov/go-lighter/internal/codec"
	"github.com/tonymontanov/go-lighter/internal/lterr"
	"github.com/tonymontanov/go-lighter/internal/ltlog"
)

const (
	// APIPrefix — path prefix of every endpoint.
	APIPrefix string = "/api/v1/"
	// maxErrorBodyBytes — how much of a non-JSON error body is kept in the error text.
	maxErrorBodyBytes int = 512
	// maxResponseBytes — hard cap of a response body (orderBookDetails of all
	// markets is ~360 KB on mainnet; histories can reach a few MB).
	maxResponseBytes int64 = 64 << 20
)

// Content types of POST requests.
const (
	contentTypeForm string = "application/x-www-form-urlencoded"
	contentTypeJSON string = "application/json"
)

// Config — transport settings.
type Config struct {
	// BaseURL — scheme + host, e.g. https://mainnet.zklighter.elliot.ai.
	BaseURL             string
	RequestTimeout      time.Duration
	MaxIdleConns        int
	MaxIdleConnsPerHost int
	IdleConnTimeout     time.Duration
	UserAgent           string
	// Proxy — optional proxy selector. nil → http.ProxyFromEnvironment.
	Proxy func(*http.Request) (*url.URL, error)
	// HTTPClient — optional fully custom client; when set, the pool / proxy
	// fields above are ignored.
	HTTPClient *http.Client
}

// Client — REST transport. Safe for concurrent use.
type Client struct {
	cfg        Config
	httpClient *http.Client
	ownsClient bool
	base       string
	logger     ltlog.Logger
}

// NewClient builds the transport. It performs no network I/O.
func NewClient(cfg Config, logger ltlog.Logger) *Client {
	if logger == nil {
		logger = ltlog.Noop()
	}
	var client *Client = &Client{
		cfg:    cfg,
		base:   strings.TrimRight(cfg.BaseURL, "/") + APIPrefix,
		logger: logger,
	}
	if cfg.HTTPClient != nil {
		client.httpClient = cfg.HTTPClient
		return client
	}
	var proxy func(*http.Request) (*url.URL, error) = cfg.Proxy
	if proxy == nil {
		proxy = http.ProxyFromEnvironment
	}
	var transport *http.Transport = &http.Transport{
		Proxy:               proxy,
		MaxIdleConns:        cfg.MaxIdleConns,
		MaxIdleConnsPerHost: cfg.MaxIdleConnsPerHost,
		IdleConnTimeout:     cfg.IdleConnTimeout,
		ForceAttemptHTTP2:   true,
	}
	client.httpClient = &http.Client{Transport: transport, Timeout: cfg.RequestTimeout}
	client.ownsClient = true
	return client
}

// Close releases idle connections of the SDK-owned client.
func (c *Client) Close() {
	if c == nil || !c.ownsClient {
		return
	}
	c.httpClient.CloseIdleConnections()
}

// BaseURL returns the configured base URL.
func (c *Client) BaseURL() string { return c.cfg.BaseURL }

// Result — outcome of one round trip.
type Result struct {
	// Status — HTTP status (0 when no response arrived).
	Status int
	// Body — raw response body (only meaningful when Err is nil).
	Body []byte
}

// Get performs GET /api/v1/<endpoint>?<query>. query is already encoded
// (without the leading "?"); auth, when not empty, is sent as the
// authorization header.
func (c *Client) Get(ctx context.Context, endpoint string, query string, auth string) (Result, error) {
	var fullURL string = c.base + endpoint
	if query != "" {
		fullURL = fullURL + "?" + query
	}
	var req *http.Request
	var err error
	req, err = http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return Result{}, lterr.New(lterr.ErrorKindInvalidRequest, "rest: build request", err)
	}
	return c.do(req, endpoint, auth)
}

// PostForm performs POST /api/v1/<endpoint> with a form-encoded body.
func (c *Client) PostForm(ctx context.Context, endpoint string, form []byte, auth string) (Result, error) {
	return c.post(ctx, endpoint, form, contentTypeForm, auth)
}

// PostJSON performs POST /api/v1/<endpoint> with a JSON body.
func (c *Client) PostJSON(ctx context.Context, endpoint string, body []byte, auth string) (Result, error) {
	return c.post(ctx, endpoint, body, contentTypeJSON, auth)
}

// post builds and runs a POST request.
func (c *Client) post(ctx context.Context, endpoint string, body []byte, contentType string, auth string) (Result, error) {
	var req *http.Request
	var err error
	req, err = http.NewRequestWithContext(ctx, http.MethodPost, c.base+endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, lterr.New(lterr.ErrorKindInvalidRequest, "rest: build request", err)
	}
	req.Header.Set("Content-Type", contentType)
	return c.do(req, endpoint, auth)
}

// do performs one round trip and classifies the answer.
func (c *Client) do(req *http.Request, endpoint string, auth string) (Result, error) {
	if c.cfg.UserAgent != "" {
		req.Header.Set("User-Agent", c.cfg.UserAgent)
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	req.Header.Set("Accept", contentTypeJSON)

	var resp *http.Response
	var err error
	resp, err = c.httpClient.Do(req)
	if err != nil {
		return Result{}, lterr.New(lterr.ErrorKindNetwork, "rest: "+endpoint+": request failed", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var result Result = Result{Status: resp.StatusCode}
	result.Body, err = readBody(resp)
	if err != nil {
		var readErr *lterr.Error = lterr.New(lterr.ErrorKindNetwork, "rest: "+endpoint+": read response", err)
		readErr.HTTPStatus = resp.StatusCode
		return result, readErr
	}

	var code int = int(codec.GetInt(result.Body, "code"))
	if resp.StatusCode != http.StatusOK {
		if code != 0 {
			return result, lterr.FromExchange(resp.StatusCode, code, codec.GetString(result.Body, "message"))
		}
		var text string = string(result.Body)
		if len(text) > maxErrorBodyBytes {
			text = text[:maxErrorBodyBytes]
		}
		var statusErr *lterr.Error = lterr.New(lterr.MapHTTPStatus(resp.StatusCode), "rest: "+endpoint+": "+strings.TrimSpace(text), nil)
		statusErr.HTTPStatus = resp.StatusCode
		return result, statusErr
	}
	if code != 0 && code != lterr.CodeOK {
		return result, lterr.FromExchange(resp.StatusCode, code, codec.GetString(result.Body, "message"))
	}
	return result, nil
}

// readBody reads the whole body, pre-sizing the buffer from Content-Length.
func readBody(resp *http.Response) ([]byte, error) {
	var limited io.Reader = io.LimitReader(resp.Body, maxResponseBytes)
	if resp.ContentLength > 0 && resp.ContentLength < maxResponseBytes {
		var buf *bytes.Buffer = bytes.NewBuffer(make([]byte, 0, resp.ContentLength+1))
		var err error
		_, err = buf.ReadFrom(limited)
		return buf.Bytes(), err
	}
	return io.ReadAll(limited)
}
