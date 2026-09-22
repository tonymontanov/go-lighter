/*
FILE: internal/tx/modify-order.go

DESCRIPTION:
Modify-order transaction (type 17). Reference: lighter-go
txtypes.L2ModifyOrderTxInfo. Replaces base amount, price and trigger price of
the order referenced by Index (client order index or exchange order index);
side and type cannot change. The optional OrderVersion attribute (8) makes
the sequencer reject a modification that is not strictly newer than the
stored version.

  hash : chainId, 17, nonce, expiredAt, accountIndex, apiKeyIndex,
         marketIndex, index, baseAmount, price, triggerPrice
  json : {"AccountIndex","ApiKeyIndex","MarketIndex","Index","BaseAmount",
          "Price","TriggerPrice","ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// ModifyOrder — transaction type 17.
type ModifyOrder struct {
	Header
	MarketIndex  int16
	Index        int64
	BaseAmount   int64
	Price        uint32
	TriggerPrice uint32
}

// Type implements Tx.
func (t *ModifyOrder) Type() types.TxType { return types.TxTypeL2ModifyOrder }

// Head implements Tx.
func (t *ModifyOrder) Head() *Header { return &t.Header }

// Validate implements Tx.
func (t *ModifyOrder) Validate() error {
	var err error = t.Header.validate(false)
	if err != nil {
		return err
	}
	err = validateMarketIndex(t.MarketIndex)
	if err != nil {
		return err
	}
	err = validateOrderRef(t.Index, ErrClientOrderIndexTooLow, ErrClientOrderIndexTooHigh)
	if err != nil {
		return err
	}
	if t.BaseAmount != NilOrderBaseAmount && t.BaseAmount < MinOrderBaseAmount {
		return ErrBaseAmountTooLow
	}
	if t.BaseAmount > MaxOrderBaseAmount {
		return ErrBaseAmountTooHigh
	}
	if t.Price < MinOrderPrice {
		return ErrPriceTooLow
	}
	if t.Price > MaxOrderPrice {
		return ErrPriceTooHigh
	}
	return nil
}

// Hash implements Tx.
func (t *ModifyOrder) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2ModifyOrder)
	b.AddInt64(int64(t.MarketIndex))
	b.AddInt64(t.Index)
	b.AddInt64(t.BaseAmount)
	b.Add(uint64(t.Price))
	b.Add(uint64(t.TriggerPrice))
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *ModifyOrder) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = appendInt(b, "MarketIndex", int64(t.MarketIndex))
	b = appendInt(b, "Index", t.Index)
	b = appendInt(b, "BaseAmount", t.BaseAmount)
	b = appendUint(b, "Price", uint64(t.Price))
	b = appendUint(b, "TriggerPrice", uint64(t.TriggerPrice))
	return t.Header.appendTail(b, sig)
}
