/*
FILE: internal/tx/create-grouped-orders.go

DESCRIPTION:
Create-grouped-orders transaction (type 28): OTO / OCO / OTOCO groups of at
most three orders on ONE market. Reference: lighter-go
txtypes.L2CreateGroupedOrdersTxInfo (validation ported rule for rule).

  OTO   (1): [parent, child]; child base amount 0, opposite side;
  OCO   (2): [SL, TP] siblings; equal base amounts, same side, both
             reduce-only, same expiry;
  OTOCO (3): [parent, SL, TP]; children base amount 0, opposite side to
             the parent, same expiry.
Parents are limit / market orders; children are SL / TP (market or limit).

  hash : chainId, 28, nonce, expiredAt, accountIndex, apiKeyIndex,
         groupingType, agg[4] where agg = HashNoPad(order0 fields) folded
         with HashNToOne(agg, HashNoPad(orderN fields))
  json : {"AccountIndex","ApiKeyIndex","GroupingType","Orders":[{order
          fields}...],"ExpiredAt","Nonce","Sig","L2TxAttributes"}
*/

package tx

import (
	"github.com/tonymontanov/go-lighter/internal/signing"
	"github.com/tonymontanov/go-lighter/types"
)

// CreateGroupedOrders — transaction type 28.
type CreateGroupedOrders struct {
	Header
	GroupingType types.GroupingType
	// Orders — 2 or 3 orders depending on the grouping type.
	Orders []OrderInfo
}

// Type implements Tx.
func (t *CreateGroupedOrders) Type() types.TxType { return types.TxTypeL2CreateGroupedOrders }

// Head implements Tx.
func (t *CreateGroupedOrders) Head() *Header { return &t.Header }

// validateParent checks a parent order (limit or market).
func validateParent(o *OrderInfo) error {
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
	default:
		return ErrOrderTypeInvalid
	}
	return nil
}

// validateChild checks a child order (SL / TP, market or limit).
func validateChild(o *OrderInfo) error {
	switch o.Type {
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
	default:
		return ErrOrderTypeInvalid
	}
	return nil
}

// validateSiblings checks an SL + TP pair.
func validateSiblings(orders []OrderInfo) error {
	if len(orders) != 2 {
		return ErrOrderGroupSizeInvalid
	}
	var slFlag bool
	var tpFlag bool
	var i int
	for i = 0; i < len(orders); i++ {
		var err error = validateChild(&orders[i])
		if err != nil {
			return err
		}
		if orders[i].Type == types.OrderTypeStopLoss || orders[i].Type == types.OrderTypeStopLossLimit {
			slFlag = true
		} else if orders[i].Type == types.OrderTypeTakeProfit || orders[i].Type == types.OrderTypeTakeProfitLimit {
			tpFlag = true
		}
	}
	if !slFlag || !tpFlag {
		return ErrOrderTypeInvalid
	}
	return nil
}

// Validate implements Tx.
func (t *CreateGroupedOrders) Validate() error {
	var err error = t.Header.validate(false)
	if err != nil {
		return err
	}
	if len(t.Orders) == 0 || len(t.Orders) > MaxGroupedOrderCount {
		return ErrOrderGroupSizeInvalid
	}
	err = validateMarketIndex(t.Orders[0].MarketIndex)
	if err != nil {
		return err
	}
	var seen [MaxGroupedOrderCount]int64
	var seenCount int
	var i int
	for i = 0; i < len(t.Orders); i++ {
		var o *OrderInfo = &t.Orders[i]
		if o.MarketIndex != t.Orders[0].MarketIndex {
			return ErrMarketIndexMismatch
		}
		if o.ClientOrderIndex != NilClientOrderIndex {
			var j int
			for j = 0; j < seenCount; j++ {
				if seen[j] == o.ClientOrderIndex {
					return ErrClientOrderIndexDuplicate
				}
			}
			seen[seenCount] = o.ClientOrderIndex
			seenCount++
		}
		err = o.validateRanges()
		if err != nil {
			return err
		}
		if o.Price > MaxOrderPrice {
			return ErrPriceTooHigh
		}
	}
	switch t.GroupingType {
	case types.GroupingOneCancelsTheOther:
		return t.validateOCO()
	case types.GroupingOneTriggersTheOther:
		return t.validateOTO()
	case types.GroupingOneTriggersOneCancelsTheOther:
		return t.validateOTOCO()
	default:
		return ErrGroupingTypeInvalid
	}
}

// validateOCO — two siblings.
func (t *CreateGroupedOrders) validateOCO() error {
	if len(t.Orders) != 2 {
		return ErrOrderGroupSizeInvalid
	}
	if t.Orders[0].BaseAmount != t.Orders[1].BaseAmount {
		return ErrBaseAmountsNotEqual
	}
	if t.Orders[0].IsAsk != t.Orders[1].IsAsk {
		return ErrIsAskInvalid
	}
	if t.Orders[0].ReduceOnly != 1 || t.Orders[1].ReduceOnly != 1 {
		return ErrOrderReduceOnlyInvalid
	}
	if t.Orders[0].OrderExpiry != t.Orders[1].OrderExpiry {
		return ErrOrderExpiryInvalid
	}
	return validateSiblings(t.Orders)
}

// validateOTO — parent + child.
func (t *CreateGroupedOrders) validateOTO() error {
	if len(t.Orders) != 2 {
		return ErrOrderGroupSizeInvalid
	}
	if t.Orders[1].BaseAmount != NilOrderBaseAmount {
		return ErrBaseAmountNotNil
	}
	if t.Orders[0].IsAsk == t.Orders[1].IsAsk {
		return ErrIsAskInvalid
	}
	if t.Orders[0].OrderExpiry != NilOrderExpiry && t.Orders[0].OrderExpiry != t.Orders[1].OrderExpiry {
		return ErrOrderExpiryInvalid
	}
	var err error = validateParent(&t.Orders[0])
	if err != nil {
		return err
	}
	return validateChild(&t.Orders[1])
}

// validateOTOCO — parent + SL + TP.
func (t *CreateGroupedOrders) validateOTOCO() error {
	if len(t.Orders) != 3 {
		return ErrOrderGroupSizeInvalid
	}
	if t.Orders[1].BaseAmount != NilOrderBaseAmount || t.Orders[2].BaseAmount != NilOrderBaseAmount {
		return ErrBaseAmountNotNil
	}
	if t.Orders[0].IsAsk == t.Orders[1].IsAsk || t.Orders[0].IsAsk == t.Orders[2].IsAsk {
		return ErrIsAskInvalid
	}
	if t.Orders[1].OrderExpiry != t.Orders[2].OrderExpiry {
		return ErrOrderExpiryInvalid
	}
	if t.Orders[0].OrderExpiry != NilOrderExpiry && t.Orders[0].OrderExpiry != t.Orders[1].OrderExpiry {
		return ErrOrderExpiryInvalid
	}
	var err error = validateParent(&t.Orders[0])
	if err != nil {
		return err
	}
	return validateSiblings(t.Orders[1:])
}

// Hash implements Tx.
func (t *CreateGroupedOrders) Hash(chainID uint32, out *signing.Hash) {
	var b signing.HashBuilder
	t.Header.begin(&b, chainID, types.TxTypeL2CreateGroupedOrders)
	b.Add(uint64(t.GroupingType))

	var aggregated signing.Digest
	var i int
	for i = 0; i < len(t.Orders); i++ {
		var orderDigest signing.Digest
		t.Orders[i].digest(&orderDigest)
		if i == 0 {
			aggregated = orderDigest
		} else {
			var folded signing.Digest
			signing.HashTwoToOne(&aggregated, &orderDigest, &folded)
			aggregated = folded
		}
	}
	b.AddDigest(&aggregated)
	t.Header.finish(&b, out)
}

// AppendInfo implements Tx.
func (t *CreateGroupedOrders) AppendInfo(b []byte, sig *signing.Signature) []byte {
	b = t.Header.appendHead(b)
	b = appendUint(b, "GroupingType", uint64(t.GroupingType))
	b = appendKey(b, "Orders")
	b = append(b, '[')
	var i int
	for i = 0; i < len(t.Orders); i++ {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '{')
		b = t.Orders[i].appendFields(b)
		// appendFields leaves a trailing comma: replace it with the closing brace.
		b[len(b)-1] = '}'
	}
	b = append(b, ']', ',')
	return t.Header.appendTail(b, sig)
}
