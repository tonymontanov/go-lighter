/*
FILE: internal/tx/tx.go

DESCRIPTION:
Common layer of Lighter L2 transactions: the Tx interface, the header shared
by every transaction and the JSON writers of tx_info.

Every transaction has TWO wire forms that must agree with the official
signer (lighter-go types/txtypes):
  - the HASH: Poseidon2 over [chainId, txType, nonce, expiredAt,
    accountIndex, apiKeyIndex, <type-specific fields>], optionally aggregated
    with the attributes hash — this is what the API key signs and what the
    exchange recomputes (a mismatch = "invalid signature", nothing else);
  - the JSON tx_info: encoding/json of the reference structs — keys in
    struct order, the signature as base64, "L2TxAttributes" last.
Each transaction implements both in one file, field for field, and every
type is covered by vectors generated with lighter-go (vectors_generated_test.go).

Transactions in this package are SECTION-AGNOSTIC: they carry resolved market
indexes and wire integers. Sections resolve symbols and scale prices / sizes
(types.Precision), then hand the transaction to the engine.

MAIN ENTITIES:
  - Tx      : interface implemented by every transaction.
  - Header  : account, api key, expiry, nonce, attributes.
  - append* : allocation-free JSON helpers.

DEPENDENCIES:
- internal/signing: hash builder, signature encoding.
- types: TxType.
*/

package tx

import (
	"strconv"

	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// Tx — a signable L2 transaction.
type Tx interface {
	// Type returns the numeric transaction type.
	Type() types.TxType
	// Head returns the common header (nonce, expiry, ...).
	Head() *Header
	// Validate checks the payload with the rules of the reference signer.
	Validate() error
	// Hash computes the message hash for the given chain id into out.
	Hash(chainID uint32, out *signing.Hash)
	// AppendInfo appends the tx_info JSON (including the signature) to b.
	AppendInfo(b []byte, sig *signing.Signature) []byte
}

// Header — fields shared by every transaction.
type Header struct {
	// AccountIndex — account (or sub-account) the transaction acts on.
	AccountIndex int64
	// APIKeyIndex — index of the signing API key (0..254).
	APIKeyIndex uint8
	// ExpiredAt — unix ms after which the sequencer drops the transaction.
	ExpiredAt int64
	// Nonce — per-API-key nonce.
	Nonce int64
	// Attributes — optional attribute set.
	Attributes Attributes
}

// validate checks the header fields shared by every transaction type.
// allowNilAPIKey is set by cancel-all, which accepts 255.
func (h *Header) validate(allowNilAPIKey bool) error {
	var err error = h.Attributes.Validate()
	if err != nil {
		return err
	}
	if h.AccountIndex < MinAccountIndex {
		return ErrAccountIndexTooLow
	}
	if h.AccountIndex > MaxAccountIndex {
		return ErrAccountIndexTooHigh
	}
	if h.APIKeyIndex > MaxAPIKeyIndex && !(allowNilAPIKey && h.APIKeyIndex == NilAPIKeyIndex) {
		return ErrAPIKeyIndexTooHigh
	}
	if h.Nonce < MinNonce {
		return ErrNonceTooLow
	}
	if h.ExpiredAt < 0 || h.ExpiredAt > MaxTimestamp {
		return ErrExpiredAtInvalid
	}
	return nil
}

// begin starts the hash with the prefix every transaction shares:
// chainId, txType, nonce, expiredAt, accountIndex, apiKeyIndex.
func (h *Header) begin(b *signing.HashBuilder, chainID uint32, txType types.TxType) {
	b.Reset()
	b.Add(uint64(chainID))
	b.Add(uint64(txType))
	b.AddInt64(h.Nonce)
	b.AddInt64(h.ExpiredAt)
	b.AddInt64(h.AccountIndex)
	b.Add(uint64(h.APIKeyIndex))
}

// finish hashes the accumulated elements and aggregates the attributes.
func (h *Header) finish(b *signing.HashBuilder, out *signing.Hash) {
	b.Finish(out)
	h.Attributes.aggregate(out)
}

// appendKey appends `"key":`.
func appendKey(b []byte, key string) []byte {
	b = append(b, '"')
	b = append(b, key...)
	return append(b, '"', ':')
}

// appendInt appends `"key":<int>,`.
func appendInt(b []byte, key string, v int64) []byte {
	b = appendKey(b, key)
	b = strconv.AppendInt(b, v, 10)
	return append(b, ',')
}

// appendUint appends `"key":<uint>,`.
func appendUint(b []byte, key string, v uint64) []byte {
	b = appendKey(b, key)
	b = strconv.AppendUint(b, v, 10)
	return append(b, ',')
}

// appendHead appends `{"AccountIndex":A,"ApiKeyIndex":K,` — the first two
// keys of every tx_info.
func (h *Header) appendHead(b []byte) []byte {
	b = append(b, '{')
	b = appendInt(b, "AccountIndex", h.AccountIndex)
	b = appendUint(b, "ApiKeyIndex", uint64(h.APIKeyIndex))
	return b
}

// appendTail appends `"ExpiredAt":E,"Nonce":N,"Sig":"<base64>","L2TxAttributes":...}`.
func (h *Header) appendTail(b []byte, sig *signing.Signature) []byte {
	b = appendInt(b, "ExpiredAt", h.ExpiredAt)
	b = appendInt(b, "Nonce", h.Nonce)
	b = appendKey(b, "Sig")
	b = append(b, '"')
	b = sig.AppendBase64(b)
	b = append(b, '"', ',')
	b = appendKey(b, "L2TxAttributes")
	b = h.Attributes.appendJSON(b)
	return append(b, '}')
}

// validateMarketIndex checks a market index of an order-related transaction.
func validateMarketIndex(marketIndex int16) error {
	if marketIndex < MinMarketIndex || marketIndex == NilMarketIndex || marketIndex > MaxMarketIndex {
		return ErrInvalidMarketIndex
	}
	return nil
}

// validateOrderRef checks a client-order-index-or-order-index reference
// (cancel / modify "Index"). lighter-go accepts either range.
func validateOrderRef(index int64, lowErr error, highErr error) error {
	if index < MinClientOrderIndex && index < MinOrderIndex {
		return lowErr
	}
	if index > MaxClientOrderIndex && index > MaxOrderIndex {
		return highErr
	}
	return nil
}
