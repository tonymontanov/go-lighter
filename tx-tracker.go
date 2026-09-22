/*
FILE: tx-tracker.go

DESCRIPTION:
TxTracker correlates sendTx receipts with the sequencer outcomes delivered by
the account_tx stream — the second confirmation level of a transaction.

    var tracker *lighter.TxTracker = lighter.NewTxTracker(4096)
    _ = perps.Stream().WatchTransactions(ctx, tracker.Observe, nil, errHandler)
    receipt, _ := perps.Trading().CreateOrder(ctx, req, opts)
    outcome, err := tracker.Await(ctx, receipt.TxHash)     // executed or failed

Outcomes are kept in a bounded map (the oldest entries are evicted when it
is full) so a late Await still finds recent transactions; waiters registered
before the outcome arrives are woken by Observe.

CONCURRENCY:
Observe runs on the stream goroutine and must stay cheap: one map write and
a non-blocking wake-up of waiters. Await blocks on a channel.
*/

package lighter

import (
	"context"
	"sync"

	"github.com/tonymontanov/go-lighter/types"
)

// TxTracker — receipt ↔ outcome correlation. Safe for concurrent use.
type TxTracker struct {
	mu         sync.Mutex
	maxEntries int
	outcomes   map[string]types.TxOutcome
	order      []string
	head       int
	waiters    map[string][]chan types.TxOutcome
}

// NewTxTracker creates a tracker that remembers at most maxEntries outcomes
// (default 4096 when <= 0).
func NewTxTracker(maxEntries int) *TxTracker {
	if maxEntries <= 0 {
		maxEntries = 4096
	}
	return &TxTracker{
		maxEntries: maxEntries,
		outcomes:   make(map[string]types.TxOutcome, maxEntries),
		order:      make([]string, 0, maxEntries),
		waiters:    make(map[string][]chan types.TxOutcome),
	}
}

// Observe records an outcome (handler of Stream().WatchTransactions).
// Pending transactions are not recorded: only executed / failed states.
func (t *TxTracker) Observe(outcome *types.TxOutcome) {
	if outcome == nil || outcome.Hash == "" {
		return
	}
	if outcome.Status != types.TxStatusExecuted && outcome.Status != types.TxStatusFailed {
		return
	}
	t.mu.Lock()
	var known bool
	_, known = t.outcomes[outcome.Hash]
	if !known {
		if len(t.order) < t.maxEntries {
			t.order = append(t.order, outcome.Hash)
		} else {
			delete(t.outcomes, t.order[t.head])
			t.order[t.head] = outcome.Hash
			t.head = (t.head + 1) % t.maxEntries
		}
	}
	t.outcomes[outcome.Hash] = *outcome
	var waiting []chan types.TxOutcome = t.waiters[outcome.Hash]
	delete(t.waiters, outcome.Hash)
	t.mu.Unlock()
	var i int
	for i = 0; i < len(waiting); i++ {
		waiting[i] <- *outcome
	}
}

// Lookup returns a recorded outcome.
func (t *TxTracker) Lookup(hash string) (types.TxOutcome, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	var outcome, ok = t.outcomes[hash]
	return outcome, ok
}

// Await blocks until the outcome of hash is known or ctx is done.
func (t *TxTracker) Await(ctx context.Context, hash string) (types.TxOutcome, error) {
	t.mu.Lock()
	var outcome, ok = t.outcomes[hash]
	if ok {
		t.mu.Unlock()
		return outcome, nil
	}
	var ch chan types.TxOutcome = make(chan types.TxOutcome, 1)
	t.waiters[hash] = append(t.waiters[hash], ch)
	t.mu.Unlock()

	select {
	case outcome = <-ch:
		return outcome, nil
	case <-ctx.Done():
		t.mu.Lock()
		var remaining []chan types.TxOutcome = t.waiters[hash][:0]
		var i int
		for i = 0; i < len(t.waiters[hash]); i++ {
			if t.waiters[hash][i] != ch {
				remaining = append(remaining, t.waiters[hash][i])
			}
		}
		if len(remaining) == 0 {
			delete(t.waiters, hash)
		} else {
			t.waiters[hash] = remaining
		}
		t.mu.Unlock()
		return types.TxOutcome{}, ctx.Err()
	}
}
