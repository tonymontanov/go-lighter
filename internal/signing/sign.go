/*
FILE: internal/signing/sign.go

DESCRIPTION:
Schnorr signatures over the ECgFp5 curve — the signature scheme of Lighter
transactions (reference: lighter-go signer/key_manager.go, which delegates to
poseidon_crypto/signature/schnorr).

  sig = SchnorrSign(hash, key)   with   k random, r = k*G,
        e = H(r || hash), s = k - e*key;   wire = s (40 LE) || e (40 LE)

The signature is RANDOMISED (k comes from crypto/rand), so two signatures of
the same hash differ and byte-for-byte vectors need a fixed k: SignWithNonce
exposes that path (tests only — never use a predictable k in production, it
leaks the private key).

PERFORMANCE:
The scalar multiplication inside the library uses math/big and allocates
(~70 allocations, ~200 µs on Apple M4 Pro). Accepted for v1.0; an
allocation-free scalar multiplication is a separate roadmap item. Everything
around it in this package (hashing, encoding) is allocation-free.

MAIN FUNCTIONS:
  - (Signer).Sign(hash, out)              : production signature.
  - (Signer).SignWithNonce(hash, k, out)  : deterministic signature (tests).
  - Verify(publicKey, hash, sig)          : reference verifier.
  - (Signature).AppendBase64              : the "Sig" JSON value writer.
*/

package signing

import (
	"encoding/base64"
	"encoding/binary"

	curve "github.com/elliottech/poseidon_crypto/curve/ecgfp5"
	gFp5 "github.com/elliottech/poseidon_crypto/field/goldilocks_quintic_extension"
	schnorr "github.com/elliottech/poseidon_crypto/signature/schnorr"
)

// Signature — Schnorr signature in wire form: s || e, each 40 bytes little-endian.
type Signature [SignatureLength]byte

// encodeSignature writes the library signature into wire form without
// allocating (the library's ToBytes allocates three slices).
func encodeSignature(sig *schnorr.Signature, out *Signature) {
	var i int
	for i = 0; i < 5; i++ {
		binary.LittleEndian.PutUint64(out[i*8:], sig.S[i])
		binary.LittleEndian.PutUint64(out[40+i*8:], sig.E[i])
	}
}

// Sign signs a transaction hash. Randomised; see the file header.
func (s *Signer) Sign(hash *Hash, out *Signature) error {
	if !s.Enabled() {
		return ErrSignerDisabled
	}
	var sig schnorr.Signature = schnorr.SchnorrSignHashedMessage(hash.Element(), s.key)
	encodeSignature(&sig, out)
	return nil
}

// SignWithNonce signs with an explicit scalar k (40 little-endian bytes,
// reduced modulo the group order). Deterministic — FOR TESTS AND VECTOR
// GENERATION ONLY.
func (s *Signer) SignWithNonce(hash *Hash, k []byte, out *Signature) error {
	if !s.Enabled() {
		return ErrSignerDisabled
	}
	if len(k) != KeyLength {
		return ErrInvalidPrivateKey
	}
	var scalar curve.ECgFp5Scalar = curve.ScalarElementFromLittleEndianBytes(k)
	var sig schnorr.Signature = schnorr.SchnorrSignHashedMessage2(hash.Element(), s.key, scalar)
	encodeSignature(&sig, out)
	return nil
}

// Verify checks sig against the encoded public key and the hash with the
// reference verifier. Diagnostics and tests; not on the hot path.
func Verify(publicKey *[KeyLength]byte, hash *Hash, sig *Signature) bool {
	var pk gFp5.Element
	var err error
	pk, err = gFp5.FromCanonicalLittleEndianBytes(publicKey[:])
	if err != nil {
		return false
	}
	var decoded schnorr.Signature
	decoded, err = schnorr.SigFromBytes(sig[:])
	if err != nil {
		return false
	}
	return schnorr.IsSchnorrSignatureValid(pk, hash.Element(), decoded)
}

// AppendBase64 appends the standard base64 form (with padding) — the value
// encoding/json produces for a []byte, hence the "Sig" field of tx_info.
// 108 characters; no allocation when b has capacity.
func (sig *Signature) AppendBase64(b []byte) []byte {
	var encoded [108]byte
	base64.StdEncoding.Encode(encoded[:], sig[:])
	return append(b, encoded[:]...)
}
