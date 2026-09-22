/*
FILE: internal/lterr/errors.go

DESCRIPTION:
SDK error type + categories + Lighter error-code mapping. Placed in an
internal package so that any internal/* package (rest, ws, engine) can use it
without an import cycle on the root lighter package. The root package
re-exports these entities via type aliases.

LIGHTER SPECIFICS:
Failures arrive in three shapes:
 1. HTTP status != 200 with a JSON body {"code": N, "message": "..."} — the
    API server rejected the request (HTTP 400 for application errors,
    429 / 405 for rate limits). The numeric code is the machine-readable
    signal: 21xxx application errors, 23xxx rate limits, 30xxx WebSocket.
 2. HTTP 200 with {"code": N != 200, "message": "..."} — same envelope, some
    endpoints answer that way; the engine treats both alike.
 3. A transaction accepted with code 200 and later REJECTED BY THE SEQUENCER
    — that is NOT an error of the call: it is reported through the account_tx
    stream (types.TxOutcome) and the order status channels.

Codes are copied from the official "Data Structures, Constants and Errors"
page (docs/API-NOTES.md lists the source). Unknown codes map to
ErrorKindExchange with the code preserved.

See also: errors.go in the root (re-export).
*/

package lterr

import (
	"errors"
	"fmt"
)

// ErrorKind — SDK error category.
type ErrorKind uint8

const (
	// ErrorKindUnknown — fallback when the SDK could not classify the failure.
	ErrorKindUnknown ErrorKind = iota
	// ErrorKindNetwork — transport-level failures (timeout, conn reset, DNS,
	// ctx cancelled, EOF on WS, 5xx). The caller may retry with backoff.
	ErrorKindNetwork
	// ErrorKindRateLimit — HTTP 429 / 405, codes 23000..23004 and 30009 / 30010.
	ErrorKindRateLimit
	// ErrorKindAuth — signature, API key, auth token or account problems.
	// Not retryable.
	ErrorKindAuth
	// ErrorKindInvalidRequest — the request is malformed or violates exchange
	// rules that are a deterministic function of the payload. Not retryable.
	ErrorKindInvalidRequest
	// ErrorKindExchange — the exchange processed the request and rejected it
	// for a state-dependent reason (margin, liquidity, missing order, ...).
	ErrorKindExchange
)

// String — human-readable category name.
func (k ErrorKind) String() string {
	switch k {
	case ErrorKindNetwork:
		return "network"
	case ErrorKindRateLimit:
		return "rate_limit"
	case ErrorKindAuth:
		return "auth"
	case ErrorKindInvalidRequest:
		return "invalid_request"
	case ErrorKindExchange:
		return "exchange"
	default:
		return "unknown"
	}
}

// Exchange error codes the SDK reacts to (subset of the official list).
const (
	CodeOK                     int = 200
	CodeAccountNotFound        int = 21100
	CodeInvalidNonce           int = 21104
	CodeNonIncreasingNonce     int = 21105
	CodeInvalidPublicKey       int = 21108
	CodeAPIKeyNotFound         int = 21109
	CodeInvalidAPIKeyIndex     int = 21110
	CodeInvalidSignature       int = 21120
	CodeBatchTxMultipleOwner   int = 21121
	CodeTooManyTxs             int = 21506
	CodeMaxBatchTx             int = 21514
	CodeInactiveCancel         int = 21600
	CodeInvalidMarketIndex     int = 21602
	CodeInvalidOrderIndex      int = 21700
	CodeInvalidOrderOwner      int = 21707
	CodeInactiveOrder          int = 21709
	CodeInactiveOrderCancel    int = 21715
	CodeMaxOrdersPerAccount    int = 21717
	CodeMaxOrdersPerMarket     int = 21718
	CodeClientOrderIndexExists int = 21728
	CodeNotEnoughOrderMargin   int = 21739
	CodeTooManyRequests        int = 23000
	CodeTooManySubscriptions   int = 23001
	CodeTooManyAccounts        int = 23002
	CodeTooManyConnections     int = 23003
	CodeTooManyL2Withdrawals   int = 23004
	CodeWsRateLimit            int = 30009
	CodeWsTooManyInflight      int = 30010
)

// Error — unified SDK error type.
type Error struct {
	Kind       ErrorKind
	HTTPStatus int
	// Code — exchange error code ({"code": N}); 0 when the error did not come
	// from the exchange.
	Code int
	// Message — SDK message ("<section>.<Method>: <detail>") or the verbatim
	// exchange message.
	Message string
	Cause   error
}

// Error implements the error interface.
func (e *Error) Error() string {
	switch {
	case e.Code != 0 && e.Cause != nil:
		return fmt.Sprintf("lighter %s: code=%d status=%d msg=%q: %v", e.Kind, e.Code, e.HTTPStatus, e.Message, e.Cause)
	case e.Code != 0:
		return fmt.Sprintf("lighter %s: code=%d status=%d msg=%q", e.Kind, e.Code, e.HTTPStatus, e.Message)
	case e.Cause != nil:
		return fmt.Sprintf("lighter %s: status=%d msg=%q: %v", e.Kind, e.HTTPStatus, e.Message, e.Cause)
	default:
		return fmt.Sprintf("lighter %s: status=%d msg=%q", e.Kind, e.HTTPStatus, e.Message)
	}
}

// Unwrap — for errors.Is/As.
func (e *Error) Unwrap() error { return e.Cause }

// New creates a *Error without an exchange code.
func New(kind ErrorKind, msg string, cause error) *Error {
	return &Error{Kind: kind, Message: msg, Cause: cause}
}

// FromExchange builds a *Error from an exchange envelope {"code": N,
// "message": "..."} received with the given HTTP status.
func FromExchange(status int, code int, message string) *Error {
	return &Error{Kind: MapCode(status, code), HTTPStatus: status, Code: code, Message: message}
}

// IsNetwork returns true if err has category Network.
func IsNetwork(err error) bool { return matchKind(err, ErrorKindNetwork) }

// IsRateLimit returns true if err has category RateLimit.
func IsRateLimit(err error) bool { return matchKind(err, ErrorKindRateLimit) }

// IsAuth returns true if err has category Auth.
func IsAuth(err error) bool { return matchKind(err, ErrorKindAuth) }

// IsInvalidRequest returns true if err has category InvalidRequest.
func IsInvalidRequest(err error) bool { return matchKind(err, ErrorKindInvalidRequest) }

// IsExchange returns true if err has category Exchange.
func IsExchange(err error) bool { return matchKind(err, ErrorKindExchange) }

// IsCode returns true if err carries the given exchange code.
func IsCode(err error, code int) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Code == code
	}
	return false
}

// CodeOf returns the exchange code of err, or 0.
func CodeOf(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

// IsNonceError reports whether the API server rejected the nonce (21104 /
// 21105): the local nonce lane must resynchronise from nextNonce.
func IsNonceError(err error) bool {
	var code int = CodeOf(err)
	return code == CodeInvalidNonce || code == CodeNonIncreasingNonce
}

// IsMissingOrder reports whether a cancel / modify failed because the order
// is not active any more (21600 / 21709 / 21715) or does not belong to the
// account (21707). Callers usually treat this as a benign outcome.
func IsMissingOrder(err error) bool {
	var code int = CodeOf(err)
	return code == CodeInactiveCancel || code == CodeInactiveOrder || code == CodeInactiveOrderCancel || code == CodeInvalidOrderOwner || code == CodeInvalidOrderIndex
}

func matchKind(err error, kind ErrorKind) bool {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind == kind
	}
	return false
}

/*
MapCode classifies an exchange error code received with an HTTP status.

  - 429 / 405 and the 23xxx / 30009 / 30010 codes → RateLimit;
  - 21100..21110, 21120 (account, nonce, key, signature) and 401 / 403 → Auth;
  - 215xx (tx shape), 216xx (market), 21700..21745 validation-like codes → InvalidRequest;
  - state-dependent order codes (margin, limits, inactive order) → Exchange;
  - 5xx → Network; anything else → Exchange (the exchange answered).
*/
func MapCode(status int, code int) ErrorKind {
	switch {
	case status == 429 || status == 405:
		return ErrorKindRateLimit
	case code == CodeTooManyRequests, code == CodeTooManySubscriptions, code == CodeTooManyAccounts,
		code == CodeTooManyConnections, code == CodeTooManyL2Withdrawals, code == CodeWsRateLimit, code == CodeWsTooManyInflight:
		return ErrorKindRateLimit
	case code == CodeTooManyTxs:
		return ErrorKindRateLimit
	case status == 401 || status == 403:
		return ErrorKindAuth
	case code >= 21100 && code <= 21110, code == CodeInvalidSignature, code == CodeBatchTxMultipleOwner:
		return ErrorKindAuth
	case code >= 21500 && code <= 21516, code >= 21600 && code <= 21626:
		return ErrorKindInvalidRequest
	case code >= 21700 && code <= 21706, code == 21710, code == 21711, code == 21713, code == 21714,
		code == CodeClientOrderIndexExists, code == 21727, code == 21729, code == 21741, code == 21742, code == 21743, code == 21744, code == 21745:
		return ErrorKindInvalidRequest
	case code >= 21700 && code <= 21745:
		return ErrorKindExchange
	case code >= 60000 && code < 70000:
		return ErrorKindInvalidRequest
	case status >= 500:
		return ErrorKindNetwork
	case code != 0 && code != CodeOK:
		return ErrorKindExchange
	case status >= 400:
		return ErrorKindInvalidRequest
	default:
		return ErrorKindUnknown
	}
}

// MapHTTPStatus returns the SDK error category for an HTTP status code when
// the body carried no exchange envelope.
func MapHTTPStatus(status int) ErrorKind {
	switch {
	case status == 429 || status == 405:
		return ErrorKindRateLimit
	case status == 401 || status == 403:
		return ErrorKindAuth
	case status >= 500:
		return ErrorKindNetwork
	case status >= 400:
		return ErrorKindInvalidRequest
	default:
		return ErrorKindUnknown
	}
}
