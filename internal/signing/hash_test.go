package signing

import (
	"testing"

	g "github.com/elliottech/poseidon_crypto/field/goldilocks"
	p2 "github.com/elliottech/poseidon_crypto/hash/poseidon2_goldilocks_plonky2"
)

// TestSpongeMatchesReference asserts that the allocation-free sponge equals the
// reference functions of poseidon_crypto for inputs of every length up to the
// builder capacity (covers the single-block and multi-block paths).
func TestSpongeMatchesReference(t *testing.T) {
	var n int
	for n = 0; n <= MaxHashInput; n++ {
		var input = make([]g.GoldilocksField, n)
		var i int
		for i = 0; i < n; i++ {
			input[i] = g.GoldilocksField(uint64(i)*0x9e3779b97f4a7c15 + 12345)
		}
		var want = p2.HashToQuinticExtension(input)
		var got Hash
		HashToQuintic(input, &got)
		var wantBytes = want.ToLittleEndianBytes()
		var gotBytes [HashLength]byte
		got.Bytes(&gotBytes)
		if string(wantBytes) != string(gotBytes[:]) {
			t.Fatalf("n=%d: HashToQuintic mismatch", n)
		}
		var wantDigest = p2.HashNoPad(input)
		var gotDigest Digest
		HashNoPad(input, &gotDigest)
		var j int
		for j = 0; j < DigestLimbs; j++ {
			if wantDigest[j] != gotDigest[j] {
				t.Fatalf("n=%d: HashNoPad limb %d mismatch", n, j)
			}
		}
	}
}

func TestHashTwoToOneMatchesReference(t *testing.T) {
	var a, b Digest
	var i int
	for i = 0; i < DigestLimbs; i++ {
		a[i] = g.GoldilocksField(1000 + i)
		b[i] = g.GoldilocksField(2000 + i)
	}
	var want = p2.HashNToOne([]p2.HashOut{p2.HashOut(a), p2.HashOut(b)})
	var got Digest
	HashTwoToOne(&a, &b, &got)
	for i = 0; i < DigestLimbs; i++ {
		if want[i] != got[i] {
			t.Fatalf("limb %d mismatch", i)
		}
	}
}

func TestHashBuilder(t *testing.T) {
	var b HashBuilder
	b.Add(1)
	b.AddInt64(-1)
	if b.Len() != 2 || b.elems[1] != g.GoldilocksField(^uint64(0)) {
		t.Fatalf("builder state: len=%d elem=%d", b.Len(), b.elems[1])
	}
	var out Hash
	b.Finish(&out)
	var want = p2.HashToQuinticExtension([]g.GoldilocksField{1, g.GoldilocksField(^uint64(0))})
	if want.ToLittleEndianBytes()[0] != byte(out[0]) {
		t.Fatal("Finish mismatch")
	}
	b.Reset()
	if b.Len() != 0 {
		t.Fatal("Reset")
	}
	if len(out.Hex()) != 80 || string(out.AppendHex(nil)) != out.Hex() {
		t.Fatal("Hex / AppendHex")
	}
}

func BenchmarkHashToQuintic16(b *testing.B) {
	var input [16]g.GoldilocksField
	var i int
	for i = 0; i < 16; i++ {
		input[i] = g.GoldilocksField(i)
	}
	var out Hash
	b.ReportAllocs()
	for i = 0; i < b.N; i++ {
		HashToQuintic(input[:], &out)
	}
}
