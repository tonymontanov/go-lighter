/*
FILE: internal/signing/hash.go

DESCRIPTION:
Poseidon2 hashing over the Goldilocks field, as used by the Lighter
transaction hashes (reference: lighter-go types/txtypes/*.go, which call
poseidon2_goldilocks_plonky2.HashToQuinticExtension / HashNoPad / HashNToOne).

The reference functions allocate their input and output slices; the hot path
of the SDK must not, so this file re-implements the two-line sponge on top of
the exported permutation of the same package (p2.Permute) with fixed-size
stack buffers. Parity with the reference implementation is asserted by
hash_test.go on every build.

MAIN ENTITIES:
  - Hash        : 5 Goldilocks limbs = quintic extension element = message
                  hash signed by Schnorr. Limbs are kept canonical.
  - Digest      : 4 limbs, the HashOut of HashNoPad (grouped orders).
  - HashBuilder : fixed-capacity element accumulator; Add / Finish.
  - HashToQuintic / HashNoPad / HashTwoToOne : allocation-free sponge helpers.

DEPENDENCIES:
- github.com/elliottech/poseidon_crypto/field/goldilocks (GoldilocksField).
- github.com/elliottech/poseidon_crypto/hash/poseidon2_goldilocks_plonky2 (Permute).
*/

package signing

import (
	"encoding/binary"
	"encoding/hex"

	g "github.com/elliottech/poseidon_crypto/field/goldilocks"
	gFp5 "github.com/elliottech/poseidon_crypto/field/goldilocks_quintic_extension"
	p2 "github.com/elliottech/poseidon_crypto/hash/poseidon2_goldilocks_plonky2"
)

const (
	// spongeRate — RATE of the Poseidon2 sponge (elements absorbed per permutation).
	spongeRate int = p2.RATE
	// HashLimbs — number of limbs of a Hash (quintic extension degree).
	HashLimbs int = 5
	// DigestLimbs — number of limbs of a Digest (HashOut).
	DigestLimbs int = 4
	// MaxHashInput — capacity of a HashBuilder. The largest transaction
	// (approveIntegrator) hashes 15 elements; grouped orders 7 + 4.
	MaxHashInput int = 24
)

// Hash — message hash: 5 canonical Goldilocks limbs (quintic extension element).
type Hash [HashLimbs]g.GoldilocksField

// Digest — 4-limb hash output used by grouped orders.
type Digest [DigestLimbs]g.GoldilocksField

// Element returns the hash as a quintic extension element.
func (h *Hash) Element() gFp5.Element {
	return gFp5.Element{h[0], h[1], h[2], h[3], h[4]}
}

// Bytes writes the little-endian encoding (40 bytes) into dst.
func (h *Hash) Bytes(dst *[HashLength]byte) {
	var i int
	for i = 0; i < HashLimbs; i++ {
		binary.LittleEndian.PutUint64(dst[i*8:], uint64(h[i]))
	}
}

// Hex returns the lower-case hex of the little-endian encoding — the tx hash
// string returned by the exchange (allocates; not for hot paths).
func (h *Hash) Hex() string {
	var raw [HashLength]byte
	h.Bytes(&raw)
	return hex.EncodeToString(raw[:])
}

// AppendHex appends the lower-case hex form to b (80 chars, no allocation
// when b has capacity).
func (h *Hash) AppendHex(b []byte) []byte {
	var raw [HashLength]byte
	h.Bytes(&raw)
	var i int
	for i = 0; i < HashLength; i++ {
		b = append(b, hexDigits[raw[i]>>4], hexDigits[raw[i]&0x0f])
	}
	return b
}

// hexDigits — lower-case hex alphabet.
const hexDigits string = "0123456789abcdef"

// HashBuilder — fixed-capacity accumulator of field elements.
type HashBuilder struct {
	elems [MaxHashInput]g.GoldilocksField
	n     int
}

// Reset empties the builder.
func (b *HashBuilder) Reset() { b.n = 0 }

// Len returns the number of accumulated elements.
func (b *HashBuilder) Len() int { return b.n }

// Add appends one element. Values are cast exactly like lighter-go does
// (`g.GoldilocksField(v)`): the caller passes the raw integer bit pattern.
// Adding beyond the capacity is a programming error and is ignored.
func (b *HashBuilder) Add(v uint64) {
	if b.n < MaxHashInput {
		b.elems[b.n] = g.GoldilocksField(v)
		b.n++
	}
}

// AddInt64 appends a signed integer with the same cast as lighter-go.
func (b *HashBuilder) AddInt64(v int64) { b.Add(uint64(v)) }

// AddDigest appends the 4 limbs of a digest.
func (b *HashBuilder) AddDigest(d *Digest) {
	var i int
	for i = 0; i < DigestLimbs; i++ {
		b.Add(uint64(d[i]))
	}
}

// AddHash appends the 5 limbs of a hash.
func (b *HashBuilder) AddHash(h *Hash) {
	var i int
	for i = 0; i < HashLimbs; i++ {
		b.Add(uint64(h[i]))
	}
}

// Finish hashes the accumulated elements into a 5-limb Hash.
func (b *HashBuilder) Finish(out *Hash) {
	HashToQuintic(b.elems[:b.n], out)
}

// FinishDigest hashes the accumulated elements into a 4-limb Digest.
func (b *HashBuilder) FinishDigest(out *Digest) {
	HashNoPad(b.elems[:b.n], out)
}

// absorb runs the Poseidon2 sponge over input and leaves the state in perm.
// Mirrors HashNToMNoPad of the reference: RATE elements per permutation, no
// padding, state never reset between blocks.
func absorb(input []g.GoldilocksField, perm *[p2.WIDTH]g.GoldilocksField) {
	var i int
	for i = 0; i < len(input); i += spongeRate {
		var j int
		for j = 0; j < spongeRate && i+j < len(input); j++ {
			perm[j] = input[i+j]
		}
		p2.Permute(perm)
	}
}

// HashToQuintic — allocation-free HashToQuinticExtension: absorb, then squeeze
// 5 elements (all within the first RATE, so no extra permutation). Limbs are
// canonicalised, matching the byte round trip of the reference
// (ToLittleEndianBytes → FromCanonicalLittleEndianBytes).
func HashToQuintic(input []g.GoldilocksField, out *Hash) {
	var perm [p2.WIDTH]g.GoldilocksField
	absorb(input, &perm)
	var i int
	for i = 0; i < HashLimbs; i++ {
		out[i] = g.GoldilocksField(perm[i].ToCanonicalUint64())
	}
}

// HashNoPad — allocation-free HashNoPad (4 outputs). Limbs are kept exactly
// as the permutation leaves them: the reference feeds them straight into the
// next hash without canonicalising.
func HashNoPad(input []g.GoldilocksField, out *Digest) {
	var perm [p2.WIDTH]g.GoldilocksField
	absorb(input, &perm)
	var i int
	for i = 0; i < DigestLimbs; i++ {
		out[i] = perm[i]
	}
}

// HashTwoToOne — HashNoPad over the 8 limbs of two digests (HashNToOne fold).
func HashTwoToOne(a *Digest, b *Digest, out *Digest) {
	var input [2 * DigestLimbs]g.GoldilocksField
	var i int
	for i = 0; i < DigestLimbs; i++ {
		input[i] = a[i]
		input[DigestLimbs+i] = b[i]
	}
	HashNoPad(input[:], out)
}
