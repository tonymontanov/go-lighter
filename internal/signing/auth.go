/*
FILE: internal/signing/auth.go

DESCRIPTION:
Authentication tokens of the private REST endpoints and WebSocket channels
(reference: lighter-go types.ConstructAuthToken).

  message   = "<deadline unix seconds>:<account index>:<api key index>"
  elements  = bytes of message split into 8-byte little-endian chunks, the
              last chunk zero-padded (goldilocks.ArrayFromCanonicalLittleEndianBytes)
  hash      = poseidon2_goldilocks.HashToQuinticExtension(elements)
  token     = message + ":" + hex(SchnorrSign(hash))

NOTE ON THE HASH PACKAGE:
lighter-go hashes the token message with the gnark-element flavour of
Poseidon2 (poseidon2_goldilocks), NOT the plonky2 flavour used for
transactions. Both are Poseidon2 over Goldilocks; this file follows the
reference to the letter and does not assume they agree. The token is built
once per validity period (docs: at most 8 hours), so its allocations do not
matter.

The docs describe the last part of the token as "random_hex"; it is in fact
the Schnorr signature of the message (see docs/API-NOTES.md).

MAIN FUNCTIONS:
  - AuthToken(signer, deadlineUnix, accountIndex, apiKeyIndex) (string, error)
*/

package signing

import (
	"encoding/hex"
	"strconv"

	g "github.com/elliottech/poseidon_crypto/field/goldilocks"
	gFp5 "github.com/elliottech/poseidon_crypto/field/goldilocks_quintic_extension"
	p2gnark "github.com/elliottech/poseidon_crypto/hash/poseidon2_goldilocks"
)

// AuthToken builds an authentication token valid until deadlineUnix
// (seconds). The exchange accepts deadlines at most 8 hours ahead.
func AuthToken(signer *Signer, deadlineUnix int64, accountIndex int64, apiKeyIndex uint8) (string, error) {
	if !signer.Enabled() {
		return "", ErrSignerDisabled
	}
	var message []byte = make([]byte, 0, 48)
	message = strconv.AppendInt(message, deadlineUnix, 10)
	message = append(message, ':')
	message = strconv.AppendInt(message, accountIndex, 10)
	message = append(message, ':')
	message = strconv.AppendUint(message, uint64(apiKeyIndex), 10)

	var elems []g.Element
	var err error
	elems, err = g.ArrayFromCanonicalLittleEndianBytes(message)
	if err != nil {
		return "", err
	}
	var element gFp5.Element = p2gnark.HashToQuinticExtension(elems)
	var hash Hash = Hash{element[0], element[1], element[2], element[3], element[4]}

	var sig Signature
	err = signer.Sign(&hash, &sig)
	if err != nil {
		return "", err
	}
	var token []byte = make([]byte, 0, len(message)+1+SignatureLength*2)
	token = append(token, message...)
	token = append(token, ':')
	var sigHex [SignatureLength * 2]byte
	hex.Encode(sigHex[:], sig[:])
	token = append(token, sigHex[:]...)
	return string(token), nil
}
