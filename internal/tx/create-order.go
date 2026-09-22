/*
FILE: internal/tx/create-order.go

DESCRIPTION:
Create-order transaction (type 14). Reference: lighter-go
txtypes.L2CreateOrderTxInfo.

  hash : chainId, 14, nonce, expiredAt, accountIndex, apiKeyIndex,
         marketIndex, clientOrderIndex, baseAmount, price, isAsk, type,
         timeInForce, reduceOnly, triggerPrice, orderExpiry
  json : {"AccountIndex","ApiKeyIndex","MarketIndex","ClientOrderIndex",
          "BaseAmount","Price","IsAsk","Type","TimeInForce","ReduceOnly",
          "TriggerPrice","OrderExpiry","ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// CreateOrder — transaction type 14.
type CreateOrder struct {
	Header
	Order OrderInfo
}

// Type implements Tx.
func (t *CreateOrder) Type() types.TxType { return types.TxTypeL2CreateOrder }

// Head implements Tx.
func (t *CreateOrder) Head() *Header { return &t.Header }

// Validate implements Tx.
func (t *CreateOrder) Validate() error {
	var err error = t.Header.validate(false)
	if err != nil {
		return err
	}
	return t.Order.Validate()
}

// Hash implements Tx.
func (t *CreateOrder) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2CreateOrder)
	t.Order.addToHash(&b)
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *CreateOrder) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = t.Order.appendFields(b)
	return t.Header.appendTail(b, sig)
}
