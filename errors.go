/*
FILE: errors.go

DESCRIPTION:
Public re-export of the SDK error model. The implementation lives in
internal/lterr so that every internal package can use it without importing the
root package (import cycle). Type ALIASES keep errors.As / errors.Is working
across the boundary: lighter.Error and lterr.Error are the same type.

Lighter answers failures with numeric codes ({"code": 21104, "message":
"invalid nonce"}); the SDK classifies them into ErrorKind values and keeps
the code in Error.Code. A transaction accepted by the API server (code 200)
and later rejected by the sequencer is NOT an error of the call — see
types.TxOutcome.

USAGE:

	var err error = trading.CancelOrder(ctx, req, opts)
	if lighter.IsMissingOrder(err) {
		// the order is already gone — usually a benign outcome
	}
*/

package lighter

import "github.com/tonymontanov/go-lighter/internal/lterr"

// Error — unified SDK error type.
type Error = lterr.Error

// ErrorKind — SDK error category.
type ErrorKind = lterr.ErrorKind

// Error categories.
const (
	ErrorKindUnknown        ErrorKind = lterr.ErrorKindUnknown
	ErrorKindNetwork        ErrorKind = lterr.ErrorKindNetwork
	ErrorKindRateLimit      ErrorKind = lterr.ErrorKindRateLimit
	ErrorKindAuth           ErrorKind = lterr.ErrorKindAuth
	ErrorKindInvalidRequest ErrorKind = lterr.ErrorKindInvalidRequest
	ErrorKindExchange       ErrorKind = lterr.ErrorKindExchange
)

// Exchange error codes the SDK reacts to (see internal/lterr for the list).
const (
	CodeInvalidNonce           int = lterr.CodeInvalidNonce
	CodeNonIncreasingNonce     int = lterr.CodeNonIncreasingNonce
	CodeInvalidSignature       int = lterr.CodeInvalidSignature
	CodeAPIKeyNotFound         int = lterr.CodeAPIKeyNotFound
	CodeInactiveCancel         int = lterr.CodeInactiveCancel
	CodeInactiveOrder          int = lterr.CodeInactiveOrder
	CodeInvalidOrderIndex      int = lterr.CodeInvalidOrderIndex
	CodeClientOrderIndexExists int = lterr.CodeClientOrderIndexExists
	CodeNotEnoughOrderMargin   int = lterr.CodeNotEnoughOrderMargin
	CodeTooManyRequests        int = lterr.CodeTooManyRequests
)

// NewError creates an SDK error without an exchange code.
func NewError(kind ErrorKind, msg string, cause error) *Error {
	return lterr.New(kind, msg, cause)
}

// IsNetwork reports whether err is a network-class error.
func IsNetwork(err error) bool { return lterr.IsNetwork(err) }

// IsRateLimit reports whether err is a rate-limit error.
func IsRateLimit(err error) bool { return lterr.IsRateLimit(err) }

// IsAuth reports whether err is an authentication / key / signature error.
func IsAuth(err error) bool { return lterr.IsAuth(err) }

// IsInvalidRequest reports whether err is a request-validation error.
func IsInvalidRequest(err error) bool { return lterr.IsInvalidRequest(err) }

// IsExchange reports whether err is a state-dependent exchange rejection.
func IsExchange(err error) bool { return lterr.IsExchange(err) }

// IsCode reports whether err carries the given exchange code.
func IsCode(err error, code int) bool { return lterr.IsCode(err, code) }

// CodeOf returns the exchange code of err, or 0.
func CodeOf(err error) int { return lterr.CodeOf(err) }

// IsNonceError reports whether the API server rejected the nonce; the SDK
// has already scheduled a resynchronisation of the key's nonce.
func IsNonceError(err error) bool { return lterr.IsNonceError(err) }

// IsMissingOrder reports whether a cancel / modify failed because the order
// is not active any more or does not belong to the account.
func IsMissingOrder(err error) bool { return lterr.IsMissingOrder(err) }
