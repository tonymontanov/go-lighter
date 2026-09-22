/*
FILE: internal/tx/cancel-all-orders.go

DESCRIPTION:
Cancel-all-orders transaction (type 16). Reference: lighter-go
txtypes.L2CancelAllOrdersTxInfo.

  TimeInForce 0 immediate  : Time must be 0; the market scope attribute (5)
                             is allowed;
  TimeInForce 1 scheduled  : dead man's switch at Time (unix ms); no market
                             scope;
  TimeInForce 2 abort      : disarm; Time must be 0.
ApiKeyIndex 255 is accepted ("any key of the account").

  hash : chainId, 16, nonce, expiredAt, accountIndex, apiKeyIndex, timeInForce, time
  json : {"AccountIndex","ApiKeyIndex","TimeInForce","Time","ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// CancelAllOrders — transaction type 16.
type CancelAllOrders struct {
	Header
	TimeInForce types.CancelAllTimeInForce
	// Time — unix ms of a scheduled cancel; 0 otherwise.
	Time int64
}

// Type implements Tx.
func (t *CancelAllOrders) Type() types.TxType { return types.TxTypeL2CancelAllOrders }

// Head implements Tx.
func (t *CancelAllOrders) Head() *Header { return &t.Header }

// Validate implements Tx.
func (t *CancelAllOrders) Validate() error {
	var err error = t.Header.validate(true)
	if err != nil {
		return err
	}
	if t.Attributes.HasCancelAllMarket && t.TimeInForce != types.CancelAllImmediate && t.Attributes.CancelAllMarketIndex != NilMarketIndex {
		return ErrCancelAllMarketIndexCantBeSchedule
	}
	switch t.TimeInForce {
	case types.CancelAllImmediate:
		if t.Time != NilOrderExpiry {
			return ErrCancelAllTimeIsNotNil
		}
	case types.CancelAllScheduled:
		if t.Time < MinOrderExpiry || t.Time > MaxOrderExpiry {
			return ErrCancelAllTimeIsNotInRange
		}
	case types.CancelAllAbort:
		if t.Time != 0 {
			return ErrCancelAllTimeIsNotNil
		}
	default:
		return ErrInvalidCancelAllTimeInForce
	}
	return nil
}

// Hash implements Tx.
func (t *CancelAllOrders) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2CancelAllOrders)
	b.Add(uint64(t.TimeInForce))
	b.AddInt64(t.Time)
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *CancelAllOrders) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = appendUint(b, "TimeInForce", uint64(t.TimeInForce))
	b = appendInt(b, "Time", t.Time)
	return t.Header.appendTail(b, sig)
}
