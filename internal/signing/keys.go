/*
FILE: internal/signing/keys.go

DESCRIPTION:
API key material of Lighter: a private key is a scalar of the ECgFp5 curve
(40 bytes, little-endian), the public key is the encoded curve point
G * scalar (also 40 bytes, little-endian). The exchange identifies a key by
(account_index, api_key_index) and stores only the public key (registered by
a changePubKey transaction).

Signer holds one private key. It never leaves the struct: there is no
getter, String() is redacted and no error produced here embeds key material.
A Signer created from an empty key is DISABLED: public endpoints keep working
and every signing call returns ErrSignerDisabled.

FORMAT (lighter-go client.NewTxClient / client.GenerateAPIKey):
  private key : hex of the 40 little-endian scalar bytes, optional "0x";
  public key  : hex of the 40 little-endian point bytes. The apikeys endpoint
                returns it WITHOUT the "0x" prefix; lighter-go's GenerateAPIKey
                prints both with the prefix.
Scalars are reduced modulo the group order on input, exactly like
curve.ScalarElementFromLittleEndianBytes does in lighter-go.

MAIN FUNCTIONS:
  - NewSigner(privateKeyHex)   : constructor; "" → disabled signer.
  - GenerateKey()              : fresh random key pair (hex, with "0x").
  - (Signer).PublicKeyHex      : 80 lower-case hex chars, no prefix.
  - (Signer).Sign / SignWithNonce : see sign.go.

DEPENDENCIES:
- github.com/elliottech/poseidon_crypto/curve/ecgfp5: scalar / point arithmetic.
- github.com/elliottech/poseidon_crypto/signature/schnorr: public key derivation.
*/

package signing

import (
	"encoding/hex"
	"errors"

	curve "github.com/elliottech/poseidon_crypto/curve/ecgfp5"
	gFp5 "github.com/elliottech/poseidon_crypto/field/goldilocks_quintic_extension"
	schnorr "github.com/elliottech/poseidon_crypto/signature/schnorr"
)

// Sentinel errors of the signer.
var (
	// ErrSignerDisabled — a signing call was made on a Signer without a key.
	ErrSignerDisabled error = errors.New("signing: signer is disabled (no private key configured)")
	// ErrInvalidPrivateKey — the key is not 40 bytes of hex. The message
	// intentionally carries no key material.
	ErrInvalidPrivateKey error = errors.New("signing: invalid private key (expected 40 bytes of hex)")
)

const (
	// KeyLength — length of a private key and of an encoded public key.
	KeyLength int = 40
	// SignatureLength — length of a Schnorr signature (s || e, little-endian).
	SignatureLength int = 80
	// HashLength — length of a message hash (5 Goldilocks limbs, little-endian).
	HashLength int = 40
)

// Signer — holder of the signing key. Safe for concurrent use: signing does
// not mutate the key.
type Signer struct {
	key      curve.ECgFp5Scalar
	public   gFp5.Element
	pubBytes [KeyLength]byte
	enabled  bool
}

// NewSigner parses a hex private key (optional "0x" prefix). An empty string
// yields a disabled signer and a nil error.
func NewSigner(privateKeyHex string) (*Signer, error) {
	if privateKeyHex == "" {
		return &Signer{}, nil
	}
	var s string = privateKeyHex
	if len(s) >= 2 && s[0] == '0' && (s[1] == 'x' || s[1] == 'X') {
		s = s[2:]
	}
	if len(s) != KeyLength*2 {
		return nil, ErrInvalidPrivateKey
	}
	var raw [KeyLength]byte
	var i int
	for i = 0; i < KeyLength; i++ {
		var hi int = fromHexChar(s[2*i])
		var lo int = fromHexChar(s[2*i+1])
		if hi < 0 || lo < 0 {
			wipe(raw[:])
			return nil, ErrInvalidPrivateKey
		}
		raw[i] = byte(hi<<4 | lo)
	}
	var signer *Signer = newSignerFromBytes(raw[:])
	wipe(raw[:])
	return signer, nil
}

// newSignerFromBytes builds a signer from 40 little-endian scalar bytes.
func newSignerFromBytes(raw []byte) *Signer {
	var signer *Signer = &Signer{enabled: true}
	signer.key = curve.ScalarElementFromLittleEndianBytes(raw)
	signer.public = schnorr.SchnorrPkFromSk(signer.key)
	var encoded []byte = signer.public.ToLittleEndianBytes()
	copy(signer.pubBytes[:], encoded)
	return signer
}

// GenerateKey creates a fresh random key pair. Both values are hex with a
// "0x" prefix, the form printed by lighter-go's GenerateAPIKey. Register the
// public key with a changePubKey transaction before use.
func GenerateKey() (privateKeyHex string, publicKeyHex string) {
	var scalar curve.ECgFp5Scalar = curve.SampleScalar()
	var privateBytes []byte = scalar.ToLittleEndianBytes()
	var publicBytes []byte = schnorr.SchnorrPkFromSk(scalar).ToLittleEndianBytes()
	privateKeyHex = "0x" + hex.EncodeToString(privateBytes)
	publicKeyHex = "0x" + hex.EncodeToString(publicBytes)
	wipe(privateBytes)
	return privateKeyHex, publicKeyHex
}

// fromHexChar decodes one hex digit; -1 when c is not a hex digit.
func fromHexChar(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

// wipe overwrites b with zeros.
func wipe(b []byte) {
	var i int
	for i = 0; i < len(b); i++ {
		b[i] = 0
	}
}

// Enabled reports whether the signer holds a key.
func (s *Signer) Enabled() bool {
	return s != nil && s.enabled
}

// PublicKey returns the encoded public key (40 bytes, little-endian); zero
// when disabled.
func (s *Signer) PublicKey() [KeyLength]byte {
	if s == nil {
		return [KeyLength]byte{}
	}
	return s.pubBytes
}

// PublicKeyHex returns the public key as 80 lower-case hex characters without
// a prefix — the form returned by the apikeys endpoint. Empty when disabled.
func (s *Signer) PublicKeyHex() string {
	if !s.Enabled() {
		return ""
	}
	return hex.EncodeToString(s.pubBytes[:])
}

// String returns a redacted description. Never prints key material.
func (s *Signer) String() string {
	if !s.Enabled() {
		return "Signer{disabled}"
	}
	return "Signer{public=" + s.PublicKeyHex() + ", key=<redacted>}"
}

// Close zeroes the private key. The signer becomes disabled.
func (s *Signer) Close() {
	if s == nil {
		return
	}
	s.key = curve.ECgFp5Scalar{}
	s.enabled = false
}
