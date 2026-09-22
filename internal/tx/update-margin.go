/*
FILE: internal/tx/update-margin.go

DESCRIPTION:
Update-margin transaction (type 29): add USDC to, or remove USDC from, the
isolated margin of a position. Reference: lighter-go
txtypes.L2UpdateMarginTxInfo. USDCAmount is in USDC units of 1e-6 and is
hashed as two 32-bit halves.

  hash : chainId, 29, nonce, expiredAt, accountIndex, apiKeyIndex,
         marketIndex, usdcAmount & 0xFFFFFFFF, usdcAmount >> 32, direction
  json : {"AccountIndex","ApiKeyIndex","MarketIndex","USDCAmount","Direction",
          "ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// Margin update directions.
const (
	// MarginDirectionRemove — 0: move margin from the position to the account.
	MarginDirectionRemove uint8 = 0
	// MarginDirectionAdd — 1: move margin from the account to the position.
	MarginDirectionAdd uint8 = 1
)

// UpdateMargin — transaction type 29.
type UpdateMargin struct {
	Header
	MarketIndex int16
	// USDCAmount — amount in 1e-6 USDC.
	USDCAmount int64
	Direction  uint8
}

// Type implements Tx.
func (t *UpdateMargin) Type() types.TxType { return types.TxTypeL2UpdateMargin }

// Head implements Tx.
func (t *UpdateMargin) Head() *Header { return &t.Header }

// Validate implements Tx.
func (t *UpdateMargin) Validate() error {
	var err error = t.Header.validate(false)
	if err != nil {
		return err
	}
	err = validateMarketIndex(t.MarketIndex)
	if err != nil {
		return err
	}
	if t.USDCAmount == 0 {
		return ErrTransferAmountTooLow
	}
	if t.USDCAmount > MaxTransferAmount {
		return ErrTransferAmountTooHigh
	}
	if t.Direction != MarginDirectionRemove && t.Direction != MarginDirectionAdd {
		return ErrInvalidUpdateMarginDirection
	}
	return nil
}

// Hash implements Tx.
func (t *UpdateMargin) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2UpdateMargin)
	b.AddInt64(int64(t.MarketIndex))
	b.AddInt64(t.USDCAmount & 0xFFFFFFFF)
	b.AddInt64(t.USDCAmount >> 32)
	b.Add(uint64(t.Direction))
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *UpdateMargin) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = appendInt(b, "MarketIndex", int64(t.MarketIndex))
	b = appendInt(b, "USDCAmount", t.USDCAmount)
	b = appendUint(b, "Direction", uint64(t.Direction))
	return t.Header.appendTail(b, sig)
}
