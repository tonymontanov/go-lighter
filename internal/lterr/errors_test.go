package lterr

import (
	"errors"
	"testing"
)

func TestMapCode(t *testing.T) {
	var cases = []struct {
		status int
		code   int
		want   ErrorKind
	}{
		{429, 0, ErrorKindRateLimit},
		{405, 0, ErrorKindRateLimit},
		{400, CodeTooManyRequests, ErrorKindRateLimit},
		{400, CodeWsRateLimit, ErrorKindRateLimit},
		{400, CodeTooManyTxs, ErrorKindRateLimit},
		{400, CodeInvalidNonce, ErrorKindAuth},
		{400, CodeInvalidSignature, ErrorKindAuth},
		{400, CodeAPIKeyNotFound, ErrorKindAuth},
		{401, 0, ErrorKindAuth},
		{400, CodeMaxBatchTx, ErrorKindInvalidRequest},
		{400, CodeInvalidMarketIndex, ErrorKindInvalidRequest},
		{400, CodeInvalidOrderIndex, ErrorKindInvalidRequest},
		{400, CodeClientOrderIndexExists, ErrorKindInvalidRequest},
		{400, CodeNotEnoughOrderMargin, ErrorKindExchange},
		{400, CodeMaxOrdersPerMarket, ErrorKindExchange},
		{400, CodeInactiveOrder, ErrorKindExchange},
		{400, 61001, ErrorKindInvalidRequest},
		{502, 0, ErrorKindNetwork},
		{400, 99999, ErrorKindExchange},
		{400, 0, ErrorKindInvalidRequest},
		{200, 0, ErrorKindUnknown},
	}
	for _, c := range cases {
		if got := MapCode(c.status, c.code); got != c.want {
			t.Errorf("MapCode(%d, %d) = %s, want %s", c.status, c.code, got, c.want)
		}
	}
}

func TestErrorHelpers(t *testing.T) {
	var err error = FromExchange(400, CodeInvalidNonce, "invalid nonce")
	if !IsAuth(err) || !IsNonceError(err) || CodeOf(err) != CodeInvalidNonce || !IsCode(err, CodeInvalidNonce) {
		t.Fatalf("nonce error helpers failed: %v", err)
	}
	if IsMissingOrder(err) {
		t.Fatal("nonce error is not a missing order")
	}
	var missing error = FromExchange(400, CodeInactiveCancel, "given order is not an active limit order")
	if !IsMissingOrder(missing) || !IsInvalidRequest(missing) {
		t.Fatalf("missing order helpers failed: %v", missing)
	}
	var wrapped error = New(ErrorKindNetwork, "rest: request failed", errors.New("dial tcp"))
	if !IsNetwork(wrapped) || wrapped.Error() == "" || errors.Unwrap(wrapped) == nil {
		t.Fatalf("wrapped: %v", wrapped)
	}
	if IsRateLimit(errors.New("plain")) || CodeOf(errors.New("plain")) != 0 {
		t.Fatal("plain errors must not match")
	}
	if MapHTTPStatus(429) != ErrorKindRateLimit || MapHTTPStatus(503) != ErrorKindNetwork || MapHTTPStatus(404) != ErrorKindInvalidRequest || MapHTTPStatus(200) != ErrorKindUnknown {
		t.Fatal("MapHTTPStatus")
	}
	if ErrorKindExchange.String() != "exchange" || ErrorKind(99).String() != "unknown" {
		t.Fatal("String")
	}
}
