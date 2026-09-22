package markets

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/tonymontanov/go-lighter/types"
)

func TestRegistry(t *testing.T) {
	var calls int
	var fail bool
	var reg = NewRegistry(func(ctx context.Context) ([]types.MarketInfo, error) {
		calls++
		if fail {
			return nil, errors.New("boom")
		}
		return []types.MarketInfo{{Symbol: "ETH", MarketID: 0}, {Symbol: "BTC", MarketID: 1}}, nil
	})
	if reg.Peek() != nil {
		t.Fatal("Peek before load")
	}
	var ctx = context.Background()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := reg.Get(ctx); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls != 1 {
		t.Fatalf("concurrent first callers must share one load, got %d", calls)
	}
	var snap, _ = reg.Get(ctx)
	var eth, ok = snap.BySymbol("ETH")
	if !ok || eth.MarketID != 0 {
		t.Fatal("BySymbol")
	}
	var btc, ok2 = snap.ByID(1)
	if !ok2 || btc.Symbol != "BTC" || len(snap.All()) != 2 || snap.LoadedAtMs() == 0 {
		t.Fatal("ByID / All")
	}
	if _, ok = snap.BySymbol("eth"); ok {
		t.Fatal("symbols are case-sensitive")
	}
	fail = true
	if err := reg.Refresh(ctx); err == nil {
		t.Fatal("refresh error must propagate")
	}
	if reg.Peek() != snap {
		t.Fatal("failed refresh must keep the previous snapshot")
	}
	fail = false
	if err := reg.Refresh(ctx); err != nil || reg.Peek() == snap {
		t.Fatal("refresh must swap the snapshot")
	}
}
