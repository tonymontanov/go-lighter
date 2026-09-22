/*
FILE: internal/ratelimit/limiter.go

DESCRIPTION:
SDK-side rate-limit accounting for one client (one account, one IP), built
from the windows of window.go and the tables of weights.go.

BUCKETS:
  rest    — REST requests other than sendTx / sendTxBatch. Standard: count
            (limit 60) AND weighted (limit 24 000) — a request must fit both,
            which encodes "24000 / weight < 60 → the weighted limit applies".
            Premium / Plus / Builder: weighted only.
  sendTx  — sendTx / sendTxBatch. Standard and Builder: counted in `rest`
            (60 per minute). Premium: PremiumSendTxLimit(stakedLIT); Plus: 4000.
  txType  — per transaction type buckets for the documented types
            (withdraw, leverage, ...); order types have none unless
            Config.DefaultTxTypeLimit is set.
  cooldown — unix ms until which every request is refused locally after a
            429 / 405 (firewall: 60 s; API: weight / (limit / 60) s).

The limiter never blocks. Admit reports whether a request fits; the engine
decides (Config.RejectWhenRateLimited) whether to fail locally or to send
anyway and let the exchange answer. Account records what was sent and emits
the observer event.

MAIN FUNCTIONS:
  - New(cfg)
  - (Limiter).AdmitREST / AdmitSendTx  : local guards (no side effects).
  - (Limiter).AccountREST / AccountSendTx : record + observer event.
  - (Limiter).OnRateLimited(status, weight) : start a cooldown.
  - (Limiter).Snapshot                   : diagnostics.
*/

package ratelimit

import (
	"sync/atomic"
	"time"

	"github.com/tonymontanov/go-lighter/types"
)

// Transport — how a request travelled to the exchange.
type Transport uint8

const (
	// TransportREST — HTTP.
	TransportREST Transport = iota
	// TransportWS — WebSocket jsonapi/sendtx.
	TransportWS
)

// Categories of an Event.
const (
	CategoryPlace  string = "place"
	CategoryAmend  string = "amend"
	CategoryCancel string = "cancel"
	CategoryQuery  string = "query"
	CategoryMarket string = "market"
	CategoryOther  string = "other"
)

// Event — accounting event emitted after every request.
type Event struct {
	// Endpoint — REST endpoint name ("orderBookDetails", "sendTx", ...).
	Endpoint string
	// Transport — REST or WS.
	Transport Transport
	// HTTPStatus — status of the REST response; 0 for WS posts / no response.
	HTTPStatus int
	// Weight — weight charged to the weighted REST window (0 for sendTx on
	// tiers with a separate bucket).
	Weight int64
	// UsedWeight / WeightLimit — weighted REST window AFTER the request.
	UsedWeight  int64
	WeightLimit int64
	// UsedRequests / RequestLimit — unweighted request window (Standard and
	// Builder tiers; 0 / 0 otherwise).
	UsedRequests int64
	RequestLimit int64
	// UsedSendTx / SendTxLimit — sendTx bucket AFTER the request (Premium /
	// Plus; 0 / 0 otherwise).
	UsedSendTx  int64
	SendTxLimit int64
	// TxType — transaction type of a sendTx (0 for reads); TxCount — number of
	// transactions (batch size).
	TxType  types.TxType
	TxCount int
	// Category — place / amend / cancel / query / market / other.
	Category string
	// CooldownUntilMs — unix ms of the active cooldown, 0 when none.
	CooldownUntilMs int64
}

// Config — limiter settings.
type Config struct {
	Tier Tier
	// StakedLIT — whole LIT tokens staked (Premium sendTx bucket).
	StakedLIT int64
	// DefaultTxTypeLimit — per-minute limit applied to transaction types
	// without a documented bucket; 0 disables (default).
	DefaultTxTypeLimit int64
	// Observer — optional synchronous hook.
	Observer func(Event)
	// Now — clock in unix seconds (tests).
	Now func() int64
}

// Limiter — rate-limit accounting of one client. Safe for concurrent use.
type Limiter struct {
	cfg      Config
	weighted *Window
	requests *Window // nil unless the tier counts requests
	sendTx   *Window // nil unless the tier has a sendTx bucket
	txTypes  [64]*Window
	cooldown atomic.Int64 // unix ms
	nowMs    func() int64
}

// New creates a limiter for the tier in cfg.
func New(cfg Config) *Limiter {
	if cfg.Now == nil {
		cfg.Now = wallClockSeconds
	}
	var l *Limiter = &Limiter{cfg: cfg, nowMs: func() int64 { return cfg.Now() * 1000 }}
	l.weighted = NewWindowWithClock(WeightedLimit(cfg.Tier), cfg.Now)
	switch cfg.Tier {
	case TierPremium:
		l.sendTx = NewWindowWithClock(PremiumSendTxLimit(cfg.StakedLIT), cfg.Now)
	case TierPlus:
		l.sendTx = NewWindowWithClock(PlusSendTxPerMinute, cfg.Now)
	default:
		l.requests = NewWindowWithClock(StandardRequestsPerMinute, cfg.Now)
	}
	var i int
	for i = 0; i < len(l.txTypes); i++ {
		var limit int64 = TxTypeLimit(types.TxType(i))
		if limit == 0 {
			limit = cfg.DefaultTxTypeLimit
		}
		if limit > 0 {
			l.txTypes[i] = NewWindowWithClock(limit, cfg.Now)
		}
	}
	return l
}

// Tier returns the configured tier.
func (l *Limiter) Tier() Tier { return l.cfg.Tier }

// CooldownUntilMs returns the unix ms until which requests are refused, or 0.
func (l *Limiter) CooldownUntilMs() int64 {
	var until int64 = l.cooldown.Load()
	if until != 0 && until <= l.nowMs() {
		l.cooldown.CompareAndSwap(until, 0)
		return 0
	}
	return until
}

// InCooldown reports whether a cooldown is active.
func (l *Limiter) InCooldown() bool { return l.CooldownUntilMs() != 0 }

// AdmitREST reports whether a REST read of the given endpoint fits the
// budgets right now. No side effects.
func (l *Limiter) AdmitREST(endpoint string) bool {
	if l.InCooldown() {
		return false
	}
	var weight int64 = Weight(endpoint)
	if l.weighted.Used()+weight > l.weighted.Limit() {
		return false
	}
	if l.requests != nil && l.requests.Used()+1 > l.requests.Limit() {
		return false
	}
	return true
}

// AdmitSendTx reports whether a sendTx of txType (count transactions) fits.
func (l *Limiter) AdmitSendTx(txType types.TxType, count int) bool {
	if l.InCooldown() {
		return false
	}
	if l.sendTx != nil {
		if l.sendTx.Used()+1 > l.sendTx.Limit() {
			return false
		}
	} else {
		if l.requests != nil && l.requests.Used()+1 > l.requests.Limit() {
			return false
		}
		if l.weighted.Used()+Weight(EndpointSendTx) > l.weighted.Limit() {
			return false
		}
	}
	var bucket *Window = l.txTypes[int(txType)%len(l.txTypes)]
	if bucket != nil && bucket.Used()+int64(count) > bucket.Limit() {
		return false
	}
	return true
}

// AccountREST records a REST read and emits the event.
func (l *Limiter) AccountREST(endpoint string, status int, category string) {
	var weight int64 = Weight(endpoint)
	var ev Event = Event{Endpoint: endpoint, Transport: TransportREST, HTTPStatus: status, Weight: weight, Category: category}
	ev.UsedWeight = l.weighted.Add(weight)
	ev.WeightLimit = l.weighted.Limit()
	if l.requests != nil {
		ev.UsedRequests = l.requests.Add(1)
		ev.RequestLimit = l.requests.Limit()
	}
	l.finish(&ev, status, weight)
}

// AccountSendTx records a sendTx / sendTxBatch and emits the event.
func (l *Limiter) AccountSendTx(endpoint string, transport Transport, status int, txType types.TxType, count int, category string) {
	var ev Event = Event{Endpoint: endpoint, Transport: transport, HTTPStatus: status, TxType: txType, TxCount: count, Category: category}
	var weight int64
	if l.sendTx != nil {
		ev.UsedSendTx = l.sendTx.Add(1)
		ev.SendTxLimit = l.sendTx.Limit()
		ev.UsedWeight = l.weighted.Used()
		ev.WeightLimit = l.weighted.Limit()
	} else {
		weight = Weight(EndpointSendTx)
		ev.Weight = weight
		ev.UsedWeight = l.weighted.Add(weight)
		ev.WeightLimit = l.weighted.Limit()
		if l.requests != nil {
			ev.UsedRequests = l.requests.Add(1)
			ev.RequestLimit = l.requests.Limit()
		}
	}
	var bucket *Window = l.txTypes[int(txType)%len(l.txTypes)]
	if bucket != nil {
		bucket.Add(int64(count))
	}
	l.finish(&ev, status, weight)
}

// finish applies the cooldown rule and calls the observer.
func (l *Limiter) finish(ev *Event, status int, weight int64) {
	if status == 429 || status == 405 {
		l.OnRateLimited(status, weight)
	}
	ev.CooldownUntilMs = l.CooldownUntilMs()
	if l.cfg.Observer != nil {
		l.cfg.Observer(*ev)
	}
}

/*
OnRateLimited starts a cooldown after a 429 / 405 answer.

405 is treated as the firewall (60 s static); 429 as the API servers:
weight / (limit / 60) seconds, at least 100 ms. The exchange does not say
which status belongs to which layer — see docs/API-NOTES.md.
*/
func (l *Limiter) OnRateLimited(status int, weight int64) {
	var durationMs int64
	if status == 405 {
		durationMs = FirewallCooldownSeconds * 1000
	} else {
		if weight <= 0 {
			weight = defaultWeight
		}
		durationMs = weight * 60 * 1000 / l.weighted.Limit()
		if durationMs < 100 {
			durationMs = 100
		}
	}
	var until int64 = l.nowMs() + durationMs
	for {
		var current int64 = l.cooldown.Load()
		if current >= until || l.cooldown.CompareAndSwap(current, until) {
			return
		}
	}
}

// Snapshot — point-in-time view of the budgets.
type Snapshot struct {
	Tier            Tier
	UsedWeight      int64
	WeightLimit     int64
	UsedRequests    int64
	RequestLimit    int64
	UsedSendTx      int64
	SendTxLimit     int64
	CooldownUntilMs int64
}

// Snapshot returns the current budgets.
func (l *Limiter) Snapshot() Snapshot {
	var s Snapshot = Snapshot{Tier: l.cfg.Tier, UsedWeight: l.weighted.Used(), WeightLimit: l.weighted.Limit(), CooldownUntilMs: l.CooldownUntilMs()}
	if l.requests != nil {
		s.UsedRequests = l.requests.Used()
		s.RequestLimit = l.requests.Limit()
	}
	if l.sendTx != nil {
		s.UsedSendTx = l.sendTx.Used()
		s.SendTxLimit = l.sendTx.Limit()
	}
	return s
}

// ParseTier converts a configuration string to a Tier ("standard" default).
func ParseTier(s string) Tier {
	switch s {
	case "premium", "Premium", "PREMIUM":
		return TierPremium
	case "plus", "Plus", "PLUS":
		return TierPlus
	case "builder", "Builder", "BUILDER":
		return TierBuilder
	default:
		return TierStandard
	}
}

// ensure time is referenced (clock helpers live in window.go).
var _ = time.Now
