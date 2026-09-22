/*
FILE: internal/tx/attributes.go

DESCRIPTION:
L2TxAttributes — the optional attribute set of a transaction (reference:
lighter-go types/txtypes/tx_attributes.go). Each attribute has a type code, a
nil value and a range; an attribute whose value equals its nil value is
ABSENT: it is neither hashed nor serialised.

  type 1  IntegratorAccountIndex   nil 0      0..MaxAccountIndex
  type 2  IntegratorTakerFee       nil 0      0..FeeTick
  type 3  IntegratorMakerFee       nil 0      0..FeeTick
  type 4  SkipTxNonce              nil 0      1 only
  type 5  CancelAllMarketIndex     nil 255    0..MaxMarketIndex
  type 6  SelfTradeBehaviorMode    nil 0      0..3
  type 7  SelfTradeEqualityMode    nil 0      0..1
  type 8  OrderVersion             nil 0      0..MaxTimestamp

HASH (AggregateTxHash): when at least one attribute is present, the tx hash
becomes H(txHash[5] || attrHash[5]) where attrHash = H(t1, v1, ..., t4, v4)
over the present types sorted ascending and zero-padded to 4 slots.

JSON: "L2TxAttributes":null when empty, else an object keyed by the DECIMAL
type code, keys sorted as strings (encoding/json map ordering; all codes are
single digits so string order equals numeric order).
*/

package tx

import (
	"strconv"

	"github.com/tonymontanov/go-lighter/internal/signing"
)

// Attribute type codes.
const (
	AttributeTypeIntegratorAccountIndex uint8 = 1
	AttributeTypeIntegratorTakerFee     uint8 = 2
	AttributeTypeIntegratorMakerFee     uint8 = 3
	AttributeTypeSkipTxNonce            uint8 = 4
	AttributeTypeCancelAllMarketIndex   uint8 = 5
	AttributeTypeSelfTradeBehaviorMode  uint8 = 6
	AttributeTypeSelfTradeEqualityMode  uint8 = 7
	AttributeTypeOrderVersion           uint8 = 8
)

// Self-trade behaviour modes (attribute 6).
const (
	SelfTradeBehaviorExpireMaker uint8 = 0
	SelfTradeBehaviorExpireTaker uint8 = 1
	SelfTradeBehaviorCancelBoth  uint8 = 2
	SelfTradeBehaviorReduce      uint8 = 3
)

// Self-trade equality modes (attribute 7).
const (
	SelfTradeEqualityAccountIndex       uint8 = 0
	SelfTradeEqualityMasterAccountIndex uint8 = 1
)

// Attributes — attribute set of one transaction. The zero value is empty.
type Attributes struct {
	// IntegratorAccountIndex — attribute 1; 0 = absent.
	IntegratorAccountIndex int64
	// IntegratorTakerFee — attribute 2 (FeeTick denominator); 0 = absent.
	IntegratorTakerFee uint32
	// IntegratorMakerFee — attribute 3; 0 = absent.
	IntegratorMakerFee uint32
	// SkipNonce — attribute 4: the nonce may skip values (monotonic instead
	// of strictly +1).
	SkipNonce bool
	// HasCancelAllMarket / CancelAllMarketIndex — attribute 5: limit a
	// cancel-all to one market. The flag is needed because market 0 is real.
	HasCancelAllMarket   bool
	CancelAllMarketIndex int16
	// SelfTradeBehaviorMode — attribute 6; 0 (expire maker) = absent.
	SelfTradeBehaviorMode uint8
	// SelfTradeEqualityMode — attribute 7; 0 (account index) = absent.
	SelfTradeEqualityMode uint8
	// OrderVersion — attribute 8; 0 = absent.
	OrderVersion int64
}

// attributeSlot — one present attribute (type, value).
type attributeSlot struct {
	typ   uint8
	value int64
}

// collect fills slots with the present attributes in ascending type order and
// returns their count (at most 8; the validation caps it at 4).
func (a *Attributes) collect(slots *[8]attributeSlot) int {
	var n int
	if a.IntegratorAccountIndex != 0 {
		slots[n] = attributeSlot{AttributeTypeIntegratorAccountIndex, a.IntegratorAccountIndex}
		n++
	}
	if a.IntegratorTakerFee != 0 {
		slots[n] = attributeSlot{AttributeTypeIntegratorTakerFee, int64(a.IntegratorTakerFee)}
		n++
	}
	if a.IntegratorMakerFee != 0 {
		slots[n] = attributeSlot{AttributeTypeIntegratorMakerFee, int64(a.IntegratorMakerFee)}
		n++
	}
	if a.SkipNonce {
		slots[n] = attributeSlot{AttributeTypeSkipTxNonce, 1}
		n++
	}
	if a.HasCancelAllMarket && a.CancelAllMarketIndex != NilMarketIndex {
		slots[n] = attributeSlot{AttributeTypeCancelAllMarketIndex, int64(a.CancelAllMarketIndex)}
		n++
	}
	if a.SelfTradeBehaviorMode != SelfTradeBehaviorExpireMaker {
		slots[n] = attributeSlot{AttributeTypeSelfTradeBehaviorMode, int64(a.SelfTradeBehaviorMode)}
		n++
	}
	if a.SelfTradeEqualityMode != SelfTradeEqualityAccountIndex {
		slots[n] = attributeSlot{AttributeTypeSelfTradeEqualityMode, int64(a.SelfTradeEqualityMode)}
		n++
	}
	if a.OrderVersion != NilOrderVersion {
		slots[n] = attributeSlot{AttributeTypeOrderVersion, a.OrderVersion}
		n++
	}
	return n
}

// IsEmpty reports whether no attribute is present.
func (a *Attributes) IsEmpty() bool {
	var slots [8]attributeSlot
	return a.collect(&slots) == 0
}

// Validate mirrors L2TxAttributes.Validate of lighter-go.
func (a *Attributes) Validate() error {
	var slots [8]attributeSlot
	var n int = a.collect(&slots)
	if n == 0 {
		return nil
	}
	if n > NbAttributesPerTx {
		return ErrTooManyAttributes
	}
	if a.IntegratorAccountIndex < 0 || a.IntegratorAccountIndex > MaxAccountIndex {
		return ErrIntegratorAccountIndexInvalidRange
	}
	if int64(a.IntegratorTakerFee) > FeeTick || int64(a.IntegratorMakerFee) > FeeTick {
		return ErrIntegratorFeeInvalidRange
	}
	if a.HasCancelAllMarket && (a.CancelAllMarketIndex < MinMarketIndex || a.CancelAllMarketIndex > MaxMarketIndex) {
		return ErrCancelAllMarketIndexInvalidRange
	}
	if a.SelfTradeBehaviorMode > SelfTradeBehaviorReduce {
		return ErrSelfTradeBehaviorModeInvalidRange
	}
	if a.SelfTradeEqualityMode > SelfTradeEqualityMasterAccountIndex {
		return ErrSelfTradeEqualityModeInvalidRange
	}
	if a.OrderVersion < 0 || a.OrderVersion > MaxTimestamp {
		return ErrOrderVersionInvalidRange
	}
	var hasFees bool = a.IntegratorTakerFee != 0 || a.IntegratorMakerFee != 0
	if hasFees && a.IntegratorAccountIndex == 0 {
		return ErrIntegratorAccountIndexRequiredForFees
	}
	var hasSelfTrade bool = a.SelfTradeBehaviorMode != SelfTradeBehaviorExpireMaker || a.SelfTradeEqualityMode != SelfTradeEqualityAccountIndex
	if hasSelfTrade && hasFees {
		return ErrSelfTradeSpecificationNotAllowedWithFees
	}
	if a.SelfTradeBehaviorMode == SelfTradeBehaviorReduce && a.SelfTradeEqualityMode == SelfTradeEqualityMasterAccountIndex {
		return ErrReduceModeNotAllowedWithMasterEqualityMode
	}
	return nil
}

// aggregate replaces txHash with H(txHash || attrHash) when attributes are
// present (AggregateTxHash of lighter-go). Allocation-free.
func (a *Attributes) aggregate(txHash *signing.Hash) {
	var slots [8]attributeSlot
	var n int = a.collect(&slots)
	if n == 0 {
		return
	}
	var b signing.HashBuilder
	var i int
	for i = 0; i < NbAttributesPerTx; i++ {
		if i < n {
			b.Add(uint64(slots[i].typ))
			b.AddInt64(slots[i].value)
		} else {
			b.Add(0)
			b.Add(0)
		}
	}
	var attrHash signing.Hash
	b.Finish(&attrHash)

	b.Reset()
	b.AddHash(txHash)
	b.AddHash(&attrHash)
	b.Finish(txHash)
}

// appendJSON appends the value of the "L2TxAttributes" key: null or an object.
func (a *Attributes) appendJSON(out []byte) []byte {
	var slots [8]attributeSlot
	var n int = a.collect(&slots)
	if n == 0 {
		return append(out, "null"...)
	}
	out = append(out, '{')
	var i int
	for i = 0; i < n; i++ {
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, '"')
		out = strconv.AppendUint(out, uint64(slots[i].typ), 10)
		out = append(out, '"', ':')
		out = strconv.AppendInt(out, slots[i].value, 10)
	}
	return append(out, '}')
}
