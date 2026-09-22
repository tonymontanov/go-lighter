package lighter

import (
	"context"
	"testing"
	"time"

	"github.com/tonymontanov/go-lighter/types"
)

func TestTxTracker(t *testing.T) {
	var tracker = NewTxTracker(2)
	var ctx, cancel = context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	// Pending outcomes are ignored.
	tracker.Observe(&types.TxOutcome{Hash: "a", Status: types.TxStatusPending})
	if _, ok := tracker.Lookup("a"); ok {
		t.Fatal("pending must not be recorded")
	}
	// A waiter registered before the outcome is woken up.
	var done = make(chan types.TxOutcome, 1)
	go func() {
		var out, err = tracker.Await(ctx, "a")
		if err == nil {
			done <- out
		}
	}()
	time.Sleep(20 * time.Millisecond)
	tracker.Observe(&types.TxOutcome{Hash: "a", Status: types.TxStatusExecuted})
	select {
	case out := <-done:
		if !out.Executed() {
			t.Fatal("outcome")
		}
	case <-time.After(time.Second):
		t.Fatal("waiter not woken")
	}
	// Eviction keeps the newest maxEntries.
	tracker.Observe(&types.TxOutcome{Hash: "b", Status: types.TxStatusFailed, AppError: "AppErrNotEnoughOrderMargin"})
	tracker.Observe(&types.TxOutcome{Hash: "c", Status: types.TxStatusExecuted})
	if _, ok := tracker.Lookup("a"); ok {
		t.Fatal("oldest entry must be evicted")
	}
	var b, okB = tracker.Lookup("b")
	if !okB || !b.Failed() || b.AppError != "AppErrNotEnoughOrderMargin" {
		t.Fatalf("b = %+v", b)
	}
	// Await on a known hash returns immediately; a cancelled ctx returns its error.
	if out, err := tracker.Await(ctx, "c"); err != nil || !out.Executed() {
		t.Fatalf("await known: %v", err)
	}
	var short, shortCancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer shortCancel()
	if _, err := tracker.Await(short, "zzz"); err == nil {
		t.Fatal("await must honour ctx")
	}
	tracker.mu.Lock()
	var pending = len(tracker.waiters["zzz"])
	tracker.mu.Unlock()
	if pending != 0 {
		t.Fatal("cancelled waiter must be removed")
	}
}

func TestTransactionAppError(t *testing.T) {
	var transaction = types.Transaction{EventInfo: `{"a":1,"i":404,"u":123,"ae":"AppErrInvalidNonce"}`}
	if transaction.AppError() != "AppErrInvalidNonce" {
		t.Fatalf("AppError = %q", transaction.AppError())
	}
	transaction.EventInfo = `{"a":1}`
	if transaction.AppError() != "" {
		t.Fatal("missing ae")
	}
}
