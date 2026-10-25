/*
FILE: rate-limit-event.go

DESCRIPTION:
Public rate-limit accounting event.

Lighter returns NO rate-limit headers, so — like go-hyperliquid and unlike
the CEX siblings, which forward exchange headers — go-lighter accounts for
the limits on its own side and reports the result of that accounting:

  - weighted REST window (24 000 / 240 000 per minute; Standard accounts
    additionally count 60 requests per minute);
  - sendTx bucket of Premium / Plus accounts (by staked LIT);
  - per-transaction-type buckets (withdraw, leverage, ...);
  - cooldown after a 429 / 405 answer.
The tables live in internal/ratelimit/weights.go; the tier is set with
Config.RateLimit.

CONTRACT (same as the sibling SDKs):
The observer is called SYNCHRONOUSLY in the goroutine that executed the
request and blocks its return. Implementations must be O(1) — typically a
non-blocking send to a buffered channel. nil observer → zero overhead.

The SDK counts only its OWN requests. Other processes behind the same IP or
trading the same L1 address consume the same exchange budgets invisibly.
*/

package lighter

import "github.com/tonymontanov/go-lighter/internal/ratelimit"

// RateLimitEvent — accounting event emitted after every request.
type RateLimitEvent = ratelimit.Event

// RateLimitSnapshot — point-in-time view of the SDK-side budgets.
type RateLimitSnapshot = ratelimit.Snapshot

// RateLimitTier — account tier of the rate-limit policy.
type RateLimitTier = ratelimit.Tier

// Transport — how a request travelled to the exchange.
type Transport = ratelimit.Transport

// Transports.
const (
	// TransportREST — HTTP (default).
	TransportREST Transport = ratelimit.TransportREST
	// TransportWS — WebSocket jsonapi request.
	TransportWS Transport = ratelimit.TransportWS
)

// Rate-limit categories (RateLimitEvent.Category).
const (
	RateLimitCategoryPlace  string = ratelimit.CategoryPlace
	RateLimitCategoryAmend  string = ratelimit.CategoryAmend
	RateLimitCategoryCancel string = ratelimit.CategoryCancel
	RateLimitCategoryQuery  string = ratelimit.CategoryQuery
	RateLimitCategoryMarket string = ratelimit.CategoryMarket
	RateLimitCategoryOther  string = ratelimit.CategoryOther
)

// Account tiers.
const (
	RateLimitTierStandard RateLimitTier = ratelimit.TierStandard
	RateLimitTierPremium  RateLimitTier = ratelimit.TierPremium
	RateLimitTierPlus     RateLimitTier = ratelimit.TierPlus
	RateLimitTierBuilder  RateLimitTier = ratelimit.TierBuilder
)

// EndpointWeight returns the documented REST weight of an endpoint name
// (path without /api/v1/), 300 for unlisted endpoints.
func EndpointWeight(endpoint string) int64 { return ratelimit.Weight(endpoint) }

// RateLimitBudget — documented per-minute budgets of an account tier. A zero
// field means the tier has no such window.
type RateLimitBudget struct {
	// WeightedPerMinute — weighted REST window (24 000; Builder 240 000).
	WeightedPerMinute int64
	// RequestsPerMinute — unweighted request window (Standard: 60, sendTx
	// included); 0 for the other tiers.
	RequestsPerMinute int64
	// SendTxPerMinute — sendTx / sendTxBatch bucket of Premium (by staked LIT)
	// and Plus (4000) accounts; 0 for Standard and Builder.
	SendTxPerMinute int64
}

/*
TierFromName maps a tier name as the exchange or an operator spells it
("standard", "Premium", "PLUS", "builder"; case-insensitive) to a
RateLimitTier. ok == false when the name is empty or unknown — the caller
keeps its current tier instead of silently falling back to Standard.
*/
func TierFromName(name string) (RateLimitTier, bool) {
	switch lowerASCII(name) {
	case "standard":
		return RateLimitTierStandard, true
	case "premium":
		return RateLimitTierPremium, true
	case "plus":
		return RateLimitTierPlus, true
	case "builder":
		return RateLimitTierBuilder, true
	default:
		return RateLimitTierStandard, false
	}
}

// TierBudget returns the documented per-minute budgets of a tier; stakedLIT
// (whole tokens) sizes the Premium sendTx bucket.
func TierBudget(tier RateLimitTier, stakedLIT int64) RateLimitBudget {
	var budget RateLimitBudget = RateLimitBudget{WeightedPerMinute: ratelimit.WeightedLimit(tier)}
	switch tier {
	case RateLimitTierPremium:
		budget.SendTxPerMinute = ratelimit.PremiumSendTxLimit(stakedLIT)
	case RateLimitTierPlus:
		budget.SendTxPerMinute = ratelimit.PlusSendTxPerMinute
	case RateLimitTierBuilder:
		// weighted window only; sendTx is counted in it
	default:
		budget.RequestsPerMinute = ratelimit.StandardRequestsPerMinute
	}
	return budget
}

// lowerASCII lowers ASCII letters without importing strings into the hot-path
// root package (the function runs on configuration, not on requests).
func lowerASCII(s string) string {
	var out []byte = make([]byte, len(s))
	var i int
	for i = 0; i < len(s); i++ {
		var c byte = s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		out[i] = c
	}
	return string(out)
}
