package types

import "testing"

func TestPrecisionWire(t *testing.T) {
	// ETH-like market: price 2 decimals, size 4 decimals.
	var p = Precision{PriceDecimals: 2, SizeDecimals: 4}
	if p.TickSize() != MustParseFixed("0.01") || p.LotSize() != MustParseFixed("0.0001") {
		t.Fatalf("tick %s lot %s", p.TickSize(), p.LotSize())
	}
	var wire, ok = p.WirePrice(MustParseFixed("4050.00"))
	if !ok || wire != 405000 {
		t.Fatalf("WirePrice = %d %v", wire, ok)
	}
	if _, ok = p.WirePrice(MustParseFixed("4050.005")); ok {
		t.Fatal("off-grid price accepted")
	}
	if _, ok = p.WirePrice(0); ok {
		t.Fatal("zero price accepted")
	}
	if _, ok = p.WirePrice(MustParseFixed("50000000")); ok {
		t.Fatal("price above uint32 wire range accepted")
	}
	var size, sizeOK = p.WireSize(MustParseFixed("0.1"))
	if !sizeOK || size != 1000 {
		t.Fatalf("WireSize = %d %v", size, sizeOK)
	}
	if _, sizeOK = p.WireSize(MustParseFixed("0.00001")); sizeOK {
		t.Fatal("off-grid size accepted")
	}
	if p.PriceFromWire(405000) != MustParseFixed("4050") || p.SizeFromWire(1000) != MustParseFixed("0.1") {
		t.Fatal("FromWire")
	}
	if !p.Valid() || (Precision{PriceDecimals: 9}).Valid() {
		t.Fatal("Valid")
	}
}

func TestPrecisionNormalize(t *testing.T) {
	var p = Precision{PriceDecimals: 2, SizeDecimals: 4}
	var px = MustParseFixed("4050.005")
	if p.NormalizePrice(px, RoundDown) != MustParseFixed("4050.00") {
		t.Fatal("RoundDown")
	}
	if p.NormalizePrice(px, RoundUp) != MustParseFixed("4050.01") {
		t.Fatal("RoundUp")
	}
	if p.NormalizePrice(px, RoundNearest) != MustParseFixed("4050.01") {
		t.Fatal("RoundNearest tie")
	}
	if p.NormalizePrice(MustParseFixed("4050.004"), RoundNearest) != MustParseFixed("4050.00") {
		t.Fatal("RoundNearest below tie")
	}
	if p.NormalizeSize(MustParseFixed("0.12345"), RoundDown) != MustParseFixed("0.1234") {
		t.Fatal("size RoundDown")
	}
	if p.NormalizePrice(-1, RoundUp) != -1 {
		t.Fatal("negative unchanged")
	}
	// Zero-decimal size market (ZORA-like): lot = 1.
	var whole = Precision{PriceDecimals: 6, SizeDecimals: 0}
	if whole.LotSize() != FixedFromInt(1) {
		t.Fatalf("lot = %s", whole.LotSize())
	}
	var wire, ok = whole.WireSize(FixedFromInt(700))
	if !ok || wire != 700 {
		t.Fatalf("WireSize whole = %d %v", wire, ok)
	}
}

func TestMulFixed(t *testing.T) {
	var got, ok = MulFixed(MustParseFixed("4050.5"), MustParseFixed("0.1"))
	if !ok || got != MustParseFixed("405.05") {
		t.Fatalf("MulFixed = %s %v", got, ok)
	}
	if _, ok = MulFixed(MustParseFixed("90000000000"), MustParseFixed("90000000000")); ok {
		t.Fatal("overflow not detected")
	}
	got, _ = MulFixed(MustParseFixed("-2"), MustParseFixed("3"))
	if got != MustParseFixed("-6") {
		t.Fatalf("sign = %s", got)
	}
}

func BenchmarkWirePrice(b *testing.B) {
	var p = Precision{PriceDecimals: 2, SizeDecimals: 4}
	var px = MustParseFixed("4050.25")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = p.WirePrice(px)
	}
}
