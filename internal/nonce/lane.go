/*
FILE: internal/nonce/lane.go

DESCRIPTION:
Nonce lanes: one lane per (account index, API key index) — the unit the
exchange tracks nonces for.

EXCHANGE RULES (official "Signing Transactions" page):
  - nonces are per API KEY; without the SkipNonce attribute the exchange
    requires new_nonce = old_nonce + 1, checked when the API server ingests
    the transaction;
  - with SkipNonce (attribute 4) any old_nonce < new_nonce < 2^47 - 1 is
    accepted;
  - an API-level rejection (code != 200) does NOT consume the nonce; an
    accepted transaction (code == 200) consumes it even when the sequencer
    later rejects it;
  - nextNonce returns the next nonce to use for a key.

TWO MODES:
  Sequential (default) — a lane hands out old+1. Because the exchange checks
    the nonce in ARRIVAL order, sends on one lane are serialised (the engine
    holds the lane's send mutex through the round trip — the official Python
    SDK does exactly this: "hold the key's lock through the send so
    transactions on the same key reach the sequencer in nonce order").
    Parallelism comes from several API keys (several lanes, v2.0 key pool).
    Outcomes: accepted → advance; rejected by the API → roll back (the nonce is
    reused); nonce error or unknown outcome (transport failure) → resync from
    nextNonce before the next send.
  Skip — a lane hands out max(now_ms, last+1) under a lock-free CAS and marks
    every transaction with SkipNonce. No rollback bookkeeping is needed
    (monotonic values are never reused); sends need not be serialised, at the
    price of an occasional rejection when two in-flight transactions arrive
    out of order. now_ms (1.7e12) stays far below the 2^47-1 (1.4e14) cap.

MAIN FUNCTIONS:
  - NewLane(account, apiKey, mode, fetcher)
  - (Lane).Acquire(ctx, n)   : reserve n consecutive nonces (resync if needed).
  - (Lane).Release(r, outcome): report the outcome of the send.
  - (Lane).Serialized / Lock / Unlock : send serialisation in sequential mode.
  - (Lane).Invalidate         : force a resync on the next Acquire.
*/

package nonce

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Mode — nonce assignment strategy.
type Mode uint8

const (
	// ModeSequential — old+1, serialised sends, resync on failure.
	ModeSequential Mode = iota
	// ModeSkip — monotonic millisecond nonces with the SkipNonce attribute.
	ModeSkip
)

// Outcome — what happened to a send.
type Outcome uint8

const (
	// OutcomeAccepted — the API server answered code 200: nonces consumed.
	OutcomeAccepted Outcome = iota
	// OutcomeRejected — the API server rejected the request for a reason other
	// than the nonce: nonces NOT consumed, reuse them.
	OutcomeRejected
	// OutcomeNonceError — the API server rejected the nonce itself: the local
	// counter is off, resync.
	OutcomeNonceError
	// OutcomeUnknown — transport failure, the exchange may or may not have
	// ingested the request: resync.
	OutcomeUnknown
)

// unknownNonce — marker of a lane that must resync before use.
const unknownNonce int64 = -1

// ErrNoFetcher — the lane has no nextNonce fetcher and is not synchronised.
var ErrNoFetcher error = errors.New("nonce: lane is not synchronised and has no fetcher")

// Fetcher fetches the next nonce of a key from the exchange (nextNonce).
type Fetcher func(ctx context.Context, accountIndex int64, apiKeyIndex uint8) (int64, error)

// Reservation — n consecutive nonces starting at Nonce.
type Reservation struct {
	Nonce int64
	Count int
}

// Lane — nonce source of one (account, API key). Safe for concurrent use.
type Lane struct {
	accountIndex int64
	apiKeyIndex  uint8
	mode         Mode
	fetcher      Fetcher
	now          func() int64

	// next — next nonce to hand out (sequential) or the last issued value
	// (skip); unknownNonce until the first sync.
	next atomic.Int64
	// resyncs — number of resynchronisations from the exchange (diagnostics).
	resyncs atomic.Int64
	// sendMu — held by the engine through a round trip in sequential mode.
	sendMu sync.Mutex
	// syncMu — serialises resynchronisations.
	syncMu sync.Mutex
}

// wallClockMs returns the current Unix time in milliseconds.
func wallClockMs() int64 { return time.Now().UnixMilli() }

// NewLane creates a lane. No I/O happens until the first Acquire.
func NewLane(accountIndex int64, apiKeyIndex uint8, mode Mode, fetcher Fetcher) *Lane {
	var l *Lane = &Lane{accountIndex: accountIndex, apiKeyIndex: apiKeyIndex, mode: mode, fetcher: fetcher, now: wallClockMs}
	l.next.Store(unknownNonce)
	return l
}

// NewLaneWithClock — NewLane with a custom clock (tests).
func NewLaneWithClock(accountIndex int64, apiKeyIndex uint8, mode Mode, fetcher Fetcher, now func() int64) *Lane {
	var l *Lane = NewLane(accountIndex, apiKeyIndex, mode, fetcher)
	l.now = now
	return l
}

// AccountIndex returns the account of the lane.
func (l *Lane) AccountIndex() int64 { return l.accountIndex }

// APIKeyIndex returns the API key index of the lane.
func (l *Lane) APIKeyIndex() uint8 { return l.apiKeyIndex }

// Mode returns the nonce mode.
func (l *Lane) Mode() Mode { return l.mode }

// Serialized reports whether sends on this lane must hold the send lock.
func (l *Lane) Serialized() bool { return l.mode == ModeSequential }

// Lock acquires the send lock (sequential mode). Hold it through the round trip.
func (l *Lane) Lock() { l.sendMu.Lock() }

// Unlock releases the send lock.
func (l *Lane) Unlock() { l.sendMu.Unlock() }

// Resyncs returns how many times the lane resynchronised from the exchange.
func (l *Lane) Resyncs() int64 { return l.resyncs.Load() }

// Peek returns the next nonce the lane would hand out, or -1 when unknown.
func (l *Lane) Peek() int64 { return l.next.Load() }

// Invalidate forces a resynchronisation on the next Acquire.
func (l *Lane) Invalidate() { l.next.Store(unknownNonce) }

// Seed sets the next nonce explicitly (tests, callers with their own source).
func (l *Lane) Seed(next int64) { l.next.Store(next) }

// sync fetches the next nonce from the exchange. Concurrent callers share one
// fetch: the second one finds the lane synchronised after the lock.
func (l *Lane) sync(ctx context.Context) error {
	l.syncMu.Lock()
	defer l.syncMu.Unlock()
	if l.next.Load() != unknownNonce {
		return nil
	}
	if l.fetcher == nil {
		return ErrNoFetcher
	}
	var next int64
	var err error
	next, err = l.fetcher(ctx, l.accountIndex, l.apiKeyIndex)
	if err != nil {
		return err
	}
	l.next.Store(next)
	l.resyncs.Add(1)
	return nil
}

// Acquire reserves n consecutive nonces (n >= 1).
func (l *Lane) Acquire(ctx context.Context, n int) (Reservation, error) {
	if n < 1 {
		n = 1
	}
	if l.mode == ModeSkip {
		return l.acquireSkip(n), nil
	}
	for {
		var current int64 = l.next.Load()
		if current == unknownNonce {
			var err error = l.sync(ctx)
			if err != nil {
				return Reservation{}, err
			}
			continue
		}
		if l.next.CompareAndSwap(current, current+int64(n)) {
			return Reservation{Nonce: current, Count: n}, nil
		}
	}
}

// acquireSkip hands out max(now_ms, last+1) .. +n-1 under a CAS loop.
func (l *Lane) acquireSkip(n int) Reservation {
	for {
		var last int64 = l.next.Load()
		var first int64 = l.now()
		if first <= last {
			first = last + 1
		}
		var newLast int64 = first + int64(n) - 1
		if l.next.CompareAndSwap(last, newLast) {
			return Reservation{Nonce: first, Count: n}
		}
	}
}

/*
Release reports the outcome of a send made with r.

Sequential mode: Rejected rolls the counter back to r.Nonce when r was the
most recent reservation (always true under the send lock); NonceError and
Unknown invalidate the lane. Skip mode: nothing to do — values are never
reused, and a rejected value simply stays unused.
*/
func (l *Lane) Release(r Reservation, outcome Outcome) {
	if l.mode == ModeSkip {
		return
	}
	switch outcome {
	case OutcomeAccepted:
		return
	case OutcomeRejected:
		l.next.CompareAndSwap(r.Nonce+int64(r.Count), r.Nonce)
	default:
		l.Invalidate()
	}
}
