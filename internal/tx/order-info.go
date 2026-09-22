/*
FILE: internal/tx/order-info.go

DESCRIPTION:
OrderInfo — the order payload shared by the create-order transaction (type 14)
and the create-grouped-orders transaction (type 28); reference:
lighter-go txtypes.OrderInfo and the per-type Validate switches.

VALIDATION MATRIX (Validate of L2CreateOrderTxInfo):
  market     : IOC only, no expiry, no trigger price;
  limit      : no trigger price; IOC ⇒ no expiry; GTT / post-only ⇒ expiry;
  SL / TP    : IOC only, trigger price and expiry required;
  SL-limit / TP-limit : trigger price and expiry required (any tif);
  TWAP       : GTT only, no trigger price, expiry required.
BaseAmount may be 0 only for reduce-only orders (close the whole position).
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// OrderInfo — wire form of one order.
type OrderInfo struct {
	MarketIndex      int16
	ClientOrderIndex int64
	BaseAmount       int64
	Price            uint32
	IsAsk            uint8
	Type             types.OrderType
	TimeInForce      types.TimeInForce
	ReduceOnly       uint8
	TriggerPrice     uint32
	OrderExpiry      int64
}

// validateRanges checks the field ranges shared by single and grouped orders.
func (o *OrderInfo) validateRanges() error {
	if o.ClientOrderIndex != NilClientOrderIndex {
		if o.ClientOrderIndex < MinClientOrderIndex {
			return ErrClientOrderIndexTooLow
		}
		if o.ClientOrderIndex > MaxClientOrderIndex {
			return ErrClientOrderIndexTooHigh
		}
	}
	if o.ReduceOnly != 1 && o.BaseAmount == NilOrderBaseAmount {
		return ErrBaseAmountTooLow
	}
	if o.BaseAmount != NilOrderBaseAmount && o.BaseAmount < MinOrderBaseAmount {
		return ErrBaseAmountTooLow
	}
	if o.BaseAmount > MaxOrderBaseAmount {
		return ErrBaseAmountTooHigh
	}
	if o.Price < MinOrderPrice {
		return ErrPriceTooLow
	}
	if o.IsAsk != 0 && o.IsAsk != 1 {
		return ErrIsAskInvalid
	}
	if !o.TimeInForce.Valid() {
		return ErrOrderTimeInForceInvalid
	}
	if o.ReduceOnly != 0 && o.ReduceOnly != 1 {
		return ErrOrderReduceOnlyInvalid
	}
	if (o.OrderExpiry < MinOrderExpiry || o.OrderExpiry > MaxOrderExpiry) && o.OrderExpiry != NilOrderExpiry {
		return ErrOrderExpiryInvalid
	}
	return nil
}

// validateType checks the type-specific combination of tif / expiry / trigger.
func (o *OrderInfo) validateType() error {
	switch o.Type {
	case types.OrderTypeMarket:
		if o.TimeInForce != types.TimeInForceIOC {
			return ErrOrderTimeInForceInvalid
		} else if o.OrderExpiry != NilOrderExpiry {
			return ErrOrderExpiryInvalid
		} else if o.TriggerPrice != NilOrderTriggerPrice {
			return ErrOrderTriggerPriceInvalid
		}
	case types.OrderTypeLimit:
		if o.TriggerPrice != NilOrderTriggerPrice {
			return ErrOrderTriggerPriceInvalid
		} else if o.TimeInForce == types.TimeInForceIOC && o.OrderExpiry != NilOrderExpiry {
			return ErrOrderExpiryInvalid
		} else if o.TimeInForce != types.TimeInForceIOC && o.OrderExpiry == NilOrderExpiry {
			return ErrOrderExpiryInvalid
		}
	case types.OrderTypeStopLoss, types.OrderTypeTakeProfit:
		if o.TimeInForce != types.TimeInForceIOC {
			return ErrOrderTimeInForceInvalid
		} else if o.TriggerPrice == NilOrderTriggerPrice {
			return ErrOrderTriggerPriceInvalid
		} else if o.OrderExpiry == NilOrderExpiry {
			return ErrOrderExpiryInvalid
		}
	case types.OrderTypeStopLossLimit, types.OrderTypeTakeProfitLimit:
		if o.TriggerPrice == NilOrderTriggerPrice {
			return ErrOrderTriggerPriceInvalid
		} else if o.OrderExpiry == NilOrderExpiry {
			return ErrOrderExpiryInvalid
		}
	case types.OrderTypeTWAP:
		if o.TimeInForce != types.TimeInForceGTT {
			return ErrOrderTimeInForceInvalid
		} else if o.TriggerPrice != NilOrderTriggerPrice {
			return ErrOrderTriggerPriceInvalid
		} else if o.OrderExpiry == NilOrderExpiry {
			return ErrOrderExpiryInvalid
		}
	default:
		return ErrOrderTypeInvalid
	}
	return nil
}

// Validate checks a single (non-grouped) order: ranges, type matrix, market.
func (o *OrderInfo) Validate() error {
	var err error = validateMarketIndex(o.MarketIndex)
	if err != nil {
		return err
	}
	err = o.validateRanges()
	if err != nil {
		return err
	}
	if o.Price > MaxOrderPrice {
		return ErrPriceTooHigh
	}
	return o.validateType()
}

// addToHash appends the ten order fields in hash order.
func (o *OrderInfo) addToHash(b *signing.HashBuilder) {
	b.AddInt64(int64(o.MarketIndex))
	b.AddInt64(o.ClientOrderIndex)
	b.AddInt64(o.BaseAmount)
	b.Add(uint64(o.Price))
	b.Add(uint64(o.IsAsk))
	b.Add(uint64(o.Type))
	b.Add(uint64(o.TimeInForce))
	b.Add(uint64(o.ReduceOnly))
	b.Add(uint64(o.TriggerPrice))
	b.AddInt64(o.OrderExpiry)
}

// digest computes HashNoPad over the ten order fields (grouped orders).
func (o *OrderInfo) digest(out *signing.Digest) {
	var b signing.HashBuilder
	o.addToHash(&b)
	b.FinishDigest(out)
}

// appendFields appends the ten order keys, each followed by a comma.
func (o *OrderInfo) appendFields(b []byte) []byte {
	b = appendInt(b, "MarketIndex", int64(o.MarketIndex))
	b = appendInt(b, "ClientOrderIndex", o.ClientOrderIndex)
	b = appendInt(b, "BaseAmount", o.BaseAmount)
	b = appendUint(b, "Price", uint64(o.Price))
	b = appendUint(b, "IsAsk", uint64(o.IsAsk))
	b = appendUint(b, "Type", uint64(o.Type))
	b = appendUint(b, "TimeInForce", uint64(o.TimeInForce))
	b = appendUint(b, "ReduceOnly", uint64(o.ReduceOnly))
	b = appendUint(b, "TriggerPrice", uint64(o.TriggerPrice))
	b = appendInt(b, "OrderExpiry", o.OrderExpiry)
	return b
}
