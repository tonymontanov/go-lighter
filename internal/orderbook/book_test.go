package orderbook

import (
	"testing"

	"github.com/tonymontanov/go-lighter/types"
)

func lvl(price string, size string) types.OrderBookLevel {
	return types.OrderBookLevel{Price: types.MustParseFixed(price), Size: types.MustParseFixed(size)}
}

func TestBookSnapshotAndUpdates(t *testing.T) {
	var b = New(0, 0)
	if b.Synced() || b.ApplyUpdate(&types.OrderBookUpdate{Nonce: 5}) {
		t.Fatal("updates before a snapshot must be rejected")
	}
	b.ApplySnapshot(&types.OrderBookUpdate{
		Asks:  []types.OrderBookLevel{lvl("101", "1"), lvl("100", "2"), lvl("102", "0")},
		Bids:  []types.OrderBookLevel{lvl("98", "1"), lvl("99", "3")},
		Nonce: 10, LastUpdatedAtUs: 123,
	})
	var v = b.View()
	if !b.Synced() || len(v.Asks) != 2 || v.Asks[0].Price != types.MustParseFixed("100") || v.Bids[0].Price != types.MustParseFixed("99") || v.Nonce != 10 || v.LastUpdatedAtUs != 123 {
		t.Fatalf("snapshot view = %+v", v)
	}
	// Continuous update: replace, insert, remove.
	if !b.ApplyUpdate(&types.OrderBookUpdate{
		BeginNonce: 10, Nonce: 12,
		Asks: []types.OrderBookLevel{lvl("100", "5"), lvl("103", "1")},
		Bids: []types.OrderBookLevel{lvl("99", "0"), lvl("97", "2")},
	}) {
		t.Fatal("continuous update rejected")
	}
	if len(v.Asks) != 3 || v.Asks[0].Size != types.MustParseFixed("5") || v.Asks[2].Price != types.MustParseFixed("103") {
		t.Fatalf("asks = %+v", v.Asks)
	}
	if len(v.Bids) != 2 || v.Bids[0].Price != types.MustParseFixed("98") || v.Bids[1].Price != types.MustParseFixed("97") || v.Nonce != 12 {
		t.Fatalf("bids = %+v nonce=%d", v.Bids, v.Nonce)
	}
	if v.BestAsk().Price != types.MustParseFixed("100") || v.BestBid().Price != types.MustParseFixed("98") {
		t.Fatal("best levels")
	}
	// Gap: begin_nonce does not match.
	if b.ApplyUpdate(&types.OrderBookUpdate{BeginNonce: 20, Nonce: 21, Asks: []types.OrderBookLevel{lvl("100", "0")}}) {
		t.Fatal("gap must be detected")
	}
	if b.Synced() || len(v.Asks) != 3 {
		t.Fatal("a gap must leave the book untouched and unsynced")
	}
	b.Reset()
	if len(v.Asks) != 0 || len(v.Bids) != 0 || v.Nonce != 0 {
		t.Fatal("Reset")
	}
	if v.BestAsk() != nil || v.BestBid() != nil {
		t.Fatal("empty best levels must be nil")
	}
}

func TestBookUnknownNonceAndDepth(t *testing.T) {
	var b = New(3, 2)
	b.ApplySnapshot(&types.OrderBookUpdate{Asks: []types.OrderBookLevel{lvl("1", "1"), lvl("2", "1"), lvl("3", "1")}})
	if len(b.View().Asks) != 2 {
		t.Fatal("maxDepth must trim the snapshot")
	}
	// Snapshot without a nonce accepts the first update unconditionally.
	if !b.ApplyUpdate(&types.OrderBookUpdate{BeginNonce: 77, Nonce: 78, Bids: []types.OrderBookLevel{lvl("0.5", "1")}}) {
		t.Fatal("first update after a nonce-less snapshot must be accepted")
	}
	if b.ApplyUpdate(&types.OrderBookUpdate{BeginNonce: 79, Nonce: 80}) {
		t.Fatal("second update must be checked")
	}
}

func BenchmarkApplyUpdate(b *testing.B) {
	var book = New(0, 0)
	var levels = make([]types.OrderBookLevel, 0, 200)
	var i int
	for i = 0; i < 200; i++ {
		levels = append(levels, types.OrderBookLevel{Price: types.FixedFromInt(int64(1000 + i)), Size: types.FixedFromInt(1)})
	}
	book.ApplySnapshot(&types.OrderBookUpdate{Asks: levels, Nonce: 1})
	var update = &types.OrderBookUpdate{Asks: []types.OrderBookLevel{{Price: types.FixedFromInt(1100), Size: types.FixedFromInt(2)}, {Price: types.FixedFromInt(1100), Size: 0}}}
	b.ReportAllocs()
	for i = 0; i < b.N; i++ {
		update.BeginNonce = book.View().Nonce
		update.Nonce = update.BeginNonce + 1
		book.ApplyUpdate(update)
	}
}
