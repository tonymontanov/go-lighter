/*
FILE: internal/tx/update-leverage.go

DESCRIPTION:
Update-leverage transaction (type 20). Reference: lighter-go
txtypes.L2UpdateLeverageTxInfo. Leverage is expressed as an initial margin
fraction in units of 1/10000: the official Python SDK sends
int(10000 / leverage) (20x → 500). The market's min_initial_margin_fraction
bounds it from below.

  hash : chainId, 20, nonce, expiredAt, accountIndex, apiKeyIndex,
         marketIndex, initialMarginFraction, marginMode
  json : {"AccountIndex","ApiKeyIndex","MarketIndex","InitialMarginFraction",
          "MarginMode","ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// UpdateLeverage — transaction type 20.
type UpdateLeverage struct {
	Header
	MarketIndex           int16
	InitialMarginFraction uint16
	MarginMode            types.MarginMode
}

// Type implements Tx.
func (t *UpdateLeverage) Type() types.TxType { return types.TxTypeL2UpdateLeverage }

// Head implements Tx.
func (t *UpdateLeverage) Head() *Header { return &t.Header }

// Validate implements Tx.
func (t *UpdateLeverage) Validate() error {
	var err error = t.Header.validate(false)
	if err != nil {
		return err
	}
	if t.MarketIndex == NilMarketIndex {
		return ErrInvalidMarketIndex
	}
	if !t.MarginMode.Valid() {
		return ErrInvalidMarginMode
	}
	if t.InitialMarginFraction == 0 {
		return ErrInitialMarginFractionTooLow
	}
	if int64(t.InitialMarginFraction) > MarginFractionTick {
		return ErrInitialMarginFractionTooHigh
	}
	return nil
}

// Hash implements Tx.
func (t *UpdateLeverage) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2UpdateLeverage)
	b.AddInt64(int64(t.MarketIndex))
	b.Add(uint64(t.InitialMarginFraction))
	b.Add(uint64(t.MarginMode))
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *UpdateLeverage) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = appendInt(b, "MarketIndex", int64(t.MarketIndex))
	b = appendUint(b, "InitialMarginFraction", uint64(t.InitialMarginFraction))
	b = appendUint(b, "MarginMode", uint64(t.MarginMode))
	return t.Header.appendTail(b, sig)
}
