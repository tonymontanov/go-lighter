/*
FILE: internal/tx/cancel-order.go

DESCRIPTION:
Cancel-order transaction (type 15). Reference: lighter-go
txtypes.L2CancelOrderTxInfo. Index is either the client order index or the
exchange order index of the order to cancel ("you can use the same value").

  hash : chainId, 15, nonce, expiredAt, accountIndex, apiKeyIndex, marketIndex, index
  json : {"AccountIndex","ApiKeyIndex","MarketIndex","Index","ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// CancelOrder — transaction type 15.
type CancelOrder struct {
	Header
	MarketIndex int16
	// Index — client order index or exchange order index.
	Index int64
}

// Type implements Tx.
func (t *CancelOrder) Type() types.TxType { return types.TxTypeL2CancelOrder }

// Head implements Tx.
func (t *CancelOrder) Head() *Header { return &t.Header }

// Validate implements Tx.
func (t *CancelOrder) Validate() error {
	var err error = t.Header.validate(false)
	if err != nil {
		return err
	}
	err = validateMarketIndex(t.MarketIndex)
	if err != nil {
		return err
	}
	return validateOrderRef(t.Index, ErrOrderIndexTooLow, ErrOrderIndexTooHigh)
}

// Hash implements Tx.
func (t *CancelOrder) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2CancelOrder)
	b.AddInt64(int64(t.MarketIndex))
	b.AddInt64(t.Index)
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *CancelOrder) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = appendInt(b, "MarketIndex", int64(t.MarketIndex))
	b = appendInt(b, "Index", t.Index)
	return t.Header.appendTail(b, sig)
}
