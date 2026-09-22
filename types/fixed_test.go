package types

import (
	"testing"

	"github.com/shopspring/decimal"
)

func TestParseFixed(t *testing.T) {
	var cases = []struct {
		in   string
		want Fixed
		err  error
	}{
		{"0", 0, nil},
		{"1", Fixed(FixedScale), nil},
		{"2064.54", Fixed(206454000000), nil},
		{"-3.5", Fixed(-350000000), nil},
		{"0.00000001", 1, nil},
		{"0.000000001", 0, ErrFixedPrecision},
		{"0.000000010", 1, nil},
		{"", 0, ErrFixedSyntax},
		{"1e5", 0, ErrFixedSyntax},
		{"92233720368.54775807", Fixed(1<<63 - 1), nil},
		{"92233720368.54775808", 0, ErrFixedRange},
		{"99999999999", 0, ErrFixedRange},
	}
	for _, c := range cases {
		var got, err = ParseFixed(c.in)
		if err != c.err {
			t.Errorf("ParseFixed(%q) err = %v, want %v", c.in, err, c.err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseFixed(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestFixedWire(t *testing.T) {
	var cases = map[string]string{
		"0":           "0",
		"1":           "1",
		"2064.54":     "2064.54",
		"0.00010000":  "0.0001",
		"-12.5":       "-12.5",
		"100.000":     "100",
		"0.000000010": "0.00000001",
	}
	for in, want := range cases {
		if got := MustParseFixed(in).String(); got != want {
			t.Errorf("String(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestFixedJSON(t *testing.T) {
	var f Fixed
	if err := f.UnmarshalJSON([]byte(`"2064.54"`)); err != nil || f != MustParseFixed("2064.54") {
		t.Fatalf("string form: %v %d", err, f)
	}
	if err := f.UnmarshalJSON([]byte(`3024.66`)); err != nil || f != MustParseFixed("3024.66") {
		t.Fatalf("number form: %v %d", err, f)
	}
	if err := f.UnmarshalJSON([]byte(`null`)); err != nil || f != 0 {
		t.Fatalf("null: %v %d", err, f)
	}
	if err := f.UnmarshalJSON([]byte(`""`)); err != nil || f != 0 {
		t.Fatalf("empty string: %v %d", err, f)
	}
	if err := f.UnmarshalJSON([]byte(`"0.0004493591"`)); err != nil || f != MustParseFixed("0.00044935") {
		t.Fatalf("lenient truncation: %v %d", err, f)
	}
	if err := f.UnmarshalJSON([]byte(`1e-7`)); err != nil || f != 10 {
		t.Fatalf("exponent number: %v %d", err, f)
	}
	var out, _ = MustParseFixed("2064.54").MarshalJSON()
	if string(out) != `"2064.54"` {
		t.Fatalf("MarshalJSON = %s", out)
	}
}

func TestFixedConversions(t *testing.T) {
	var f, err = FixedFromFloat64(2064.54)
	if err != nil || f != MustParseFixed("2064.54") {
		t.Fatalf("FixedFromFloat64: %v %d", err, f)
	}
	if _, err = FixedFromFloat64(1e11); err != ErrFixedRange {
		t.Fatalf("range: %v", err)
	}
	f, err = FixedFromDecimal(decimal.RequireFromString("0.1234"))
	if err != nil || f != MustParseFixed("0.1234") {
		t.Fatalf("FixedFromDecimal: %v %d", err, f)
	}
	if !MustParseFixed("0.1234").Decimal().Equal(decimal.RequireFromString("0.1234")) {
		t.Fatal("Decimal round trip")
	}
	if MustParseFixed("-2").Abs() != MustParseFixed("2") || MustParseFixed("2").Neg() != MustParseFixed("-2") {
		t.Fatal("Abs / Neg")
	}
	if MustParseFixed("-2").Sign() != -1 || Fixed(0).Sign() != 0 || MustParseFixed("2").Sign() != 1 {
		t.Fatal("Sign")
	}
}

func BenchmarkFixedAppendWire(b *testing.B) {
	var f Fixed = MustParseFixed("86759.12345678")
	var buf = make([]byte, 0, 32)
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		buf = f.AppendWire(buf[:0])
	}
}

func BenchmarkParseFixedBytes(b *testing.B) {
	var in = []byte("2064.54")
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, _ = ParseFixedBytes(in)
	}
}
