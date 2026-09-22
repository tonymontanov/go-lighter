package nonce

import (
	"context"
	"errors"
	"sync"
	"testing"
)

func fetcherOf(value int64, calls *int) Fetcher {
	return func(ctx context.Context, account int64, key uint8) (int64, error) {
		*calls++
		return value, nil
	}
}

func TestSequentialLane(t *testing.T) {
	var calls int
	var lane = NewLane(1, 2, ModeSequential, fetcherOf(100, &calls))
	var ctx = context.Background()
	if lane.Peek() != -1 || !lane.Serialized() || lane.Mode() != ModeSequential || lane.AccountIndex() != 1 || lane.APIKeyIndex() != 2 {
		t.Fatal("initial state")
	}

	var r, err = lane.Acquire(ctx, 1)
	if err != nil || r.Nonce != 100 || r.Count != 1 || calls != 1 || lane.Resyncs() != 1 {
		t.Fatalf("first acquire: %+v %v calls=%d", r, err, calls)
	}
	lane.Release(r, OutcomeAccepted)
	r, _ = lane.Acquire(ctx, 3)
	if r.Nonce != 101 || r.Count != 3 || lane.Peek() != 104 {
		t.Fatalf("batch acquire: %+v peek=%d", r, lane.Peek())
	}
	lane.Release(r, OutcomeRejected)
	if lane.Peek() != 101 {
		t.Fatalf("rollback: peek=%d", lane.Peek())
	}
	r, _ = lane.Acquire(ctx, 1)
	lane.Release(r, OutcomeNonceError)
	if lane.Peek() != -1 {
		t.Fatal("nonce error must invalidate")
	}
	r, _ = lane.Acquire(ctx, 1)
	if r.Nonce != 100 || calls != 2 {
		t.Fatalf("resync: %+v calls=%d", r, calls)
	}
	lane.Release(r, OutcomeUnknown)
	if lane.Peek() != -1 {
		t.Fatal("unknown outcome must invalidate")
	}
	lane.Seed(500)
	r, _ = lane.Acquire(ctx, 0)
	if r.Nonce != 500 || r.Count != 1 {
		t.Fatalf("seed / n<1: %+v", r)
	}
}

func TestSequentialLaneFetchFailure(t *testing.T) {
	var lane = NewLane(1, 2, ModeSequential, func(ctx context.Context, account int64, key uint8) (int64, error) {
		return 0, errors.New("boom")
	})
	if _, err := lane.Acquire(context.Background(), 1); err == nil {
		t.Fatal("fetch error must propagate")
	}
	var noFetcher = NewLane(1, 2, ModeSequential, nil)
	if _, err := noFetcher.Acquire(context.Background(), 1); err != ErrNoFetcher {
		t.Fatalf("no fetcher: %v", err)
	}
}

func TestSkipLane(t *testing.T) {
	var clock int64 = 1_700_000_000_000
	var lane = NewLaneWithClock(1, 2, ModeSkip, nil, func() int64 { return clock })
	if lane.Serialized() {
		t.Fatal("skip mode must not serialise sends")
	}
	var r1, _ = lane.Acquire(context.Background(), 1)
	var r2, _ = lane.Acquire(context.Background(), 2)
	var r3, _ = lane.Acquire(context.Background(), 1)
	if r1.Nonce != clock || r2.Nonce != clock+1 || r2.Count != 2 || r3.Nonce != clock+3 {
		t.Fatalf("skip nonces: %+v %+v %+v", r1, r2, r3)
	}
	clock += 10
	var r4, _ = lane.Acquire(context.Background(), 1)
	if r4.Nonce != clock {
		t.Fatalf("clock advance: %+v", r4)
	}
	lane.Release(r4, OutcomeRejected)
	var r5, _ = lane.Acquire(context.Background(), 1)
	if r5.Nonce != clock+1 {
		t.Fatalf("skip mode never reuses: %+v", r5)
	}
}

func TestSkipLaneConcurrentUnique(t *testing.T) {
	var lane = NewLane(1, 2, ModeSkip, nil)
	var mu sync.Mutex
	var seen = map[int64]bool{}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				var r, _ = lane.Acquire(context.Background(), 1)
				mu.Lock()
				if seen[r.Nonce] {
					t.Errorf("duplicate nonce %d", r.Nonce)
				}
				seen[r.Nonce] = true
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
}

func BenchmarkAcquireSequential(b *testing.B) {
	var lane = NewLane(1, 2, ModeSequential, nil)
	lane.Seed(1)
	var ctx = context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var r, _ = lane.Acquire(ctx, 1)
		lane.Release(r, OutcomeAccepted)
	}
}

func BenchmarkAcquireSkip(b *testing.B) {
	var lane = NewLane(1, 2, ModeSkip, nil)
	var ctx = context.Background()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = lane.Acquire(ctx, 1)
	}
}
