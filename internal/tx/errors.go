/*
FILE: internal/tx/errors.go

DESCRIPTION:
Wire constants and validation errors of Lighter L2 transactions. Values are
copied from lighter-go types/txtypes/constants.go and errors.go; the SDK
validates locally exactly what the reference implementation validates, so a
transaction that passes here has the same shape the official signer would
produce.

The errors are plain sentinels: the domain layer wraps them into the SDK
error type (ErrorKindInvalidRequest) with the operation name.
*/

package tx

import "errors"

// Range constants (lighter-go constants.go).
const (
	// MinAccountIndex — accounts are non-negative; -1 is the "nil" sentinel
	// accepted by the reference validation.
	MinAccountIndex int64 = -1
	// MaxAccountIndex — (1 << 48) - 2.
	MaxAccountIndex int64 = 281474976710654
	// MaxAPIKeyIndex — 254; 255 means "all keys" in queries.
	MaxAPIKeyIndex uint8 = 254
	// NilAPIKeyIndex — 255: allowed only by cancel-all (any key of the account).
	NilAPIKeyIndex uint8 = 255

	// MinMarketIndex — first market index.
	MinMarketIndex int16 = 0
	// MaxMarketIndex — (1 << 15) - 1.
	MaxMarketIndex int16 = 1<<15 - 1
	// NilMarketIndex — 255: "all markets" sentinel; never a real market of a tx.
	NilMarketIndex int16 = 255

	// NilClientOrderIndex — 0: no client order index.
	NilClientOrderIndex int64 = 0
	// MinClientOrderIndex — 1.
	MinClientOrderIndex int64 = 1
	// MaxClientOrderIndex — (1 << 48) - 1.
	MaxClientOrderIndex int64 = 1<<48 - 1
	// MinOrderIndex — exchange order indexes start right above client ones.
	MinOrderIndex int64 = MaxClientOrderIndex + 1
	// MaxOrderIndex — (1 << 60) - 1.
	MaxOrderIndex int64 = 1<<60 - 1

	// NilOrderBaseAmount — 0: allowed for reduce-only (close the position)
	// and for grouped child orders.
	NilOrderBaseAmount int64 = 0
	// MinOrderBaseAmount — 1.
	MinOrderBaseAmount int64 = 1
	// MaxOrderBaseAmount — (1 << 48) - 1.
	MaxOrderBaseAmount int64 = 1<<48 - 1

	// NilOrderPrice — 0 (invalid for a create order).
	NilOrderPrice uint32 = 0
	// MinOrderPrice — 1.
	MinOrderPrice uint32 = 1
	// MaxOrderPrice — (1 << 32) - 1.
	MaxOrderPrice uint32 = 1<<32 - 1

	// NilOrderTriggerPrice — 0: no trigger.
	NilOrderTriggerPrice uint32 = 0
	// NilOrderExpiry — 0: no expiry (IOC / market orders).
	NilOrderExpiry int64 = 0
	// MinOrderExpiry — 1.
	MinOrderExpiry int64 = 1
	// MaxOrderExpiry — math.MaxInt64.
	MaxOrderExpiry int64 = 1<<63 - 1
	// MinOrderExpiryPeriodMs — 5 minutes: shortest order_expiry ahead of now.
	MinOrderExpiryPeriodMs int64 = 1000 * 60 * 5
	// MaxOrderExpiryPeriodMs — 30 days: longest order_expiry ahead of now.
	MaxOrderExpiryPeriodMs int64 = 1000 * 60 * 60 * 24 * 30
	// MinOrderCancelAllPeriodMs — 5 minutes: shortest scheduled cancel-all.
	MinOrderCancelAllPeriodMs int64 = 1000 * 60 * 5
	// MaxOrderCancelAllPeriodMs — 15 days: longest scheduled cancel-all.
	MaxOrderCancelAllPeriodMs int64 = 1000 * 60 * 60 * 24 * 15

	// MinNonce — 0.
	MinNonce int64 = 0
	// MaxSkipNonce — with SkipNonce, new_nonce < 2^47 - 1 must hold (docs).
	MaxSkipNonce int64 = 1<<47 - 1
	// MaxTimestamp — (1 << 48) - 1: upper bound of ExpiredAt.
	MaxTimestamp int64 = 1<<48 - 1

	// NilOrderVersion — 0: no order version.
	NilOrderVersion int64 = 0
	// MaxGroupedOrderCount — 3.
	MaxGroupedOrderCount int = 3
	// MarginFractionTick — 10_000: initial margin fraction of 1x leverage.
	MarginFractionTick int64 = 10_000
	// FeeTick — 1_000_000: fee denominator of integrator fees.
	FeeTick int64 = 1_000_000
	// MaxTransferAmount — (1 << 60) - 1 (bound of update-margin amounts).
	MaxTransferAmount int64 = 1<<60 - 1
	// NbAttributesPerTx — at most 4 attributes per transaction.
	NbAttributesPerTx int = 4
)

// Validation errors. Messages follow lighter-go so that the same failure reads
// the same in both SDKs.
var (
	ErrAccountIndexTooLow                 error = errors.New("AccountIndex should not be less than -1")
	ErrAccountIndexTooHigh                error = errors.New("AccountIndex should not be larger than 281474976710654")
	ErrAPIKeyIndexTooHigh                 error = errors.New("ApiKeyIndex should not be larger than 254")
	ErrNonceTooLow                        error = errors.New("AccountNonce should not be less than 0")
	ErrExpiredAtInvalid                   error = errors.New("ExpiredAt is invalid")
	ErrInvalidMarketIndex                 error = errors.New("MarketIndex is not valid")
	ErrMarketIndexMismatch                error = errors.New("MarketIndex should match the market index of the order")
	ErrClientOrderIndexTooLow             error = errors.New("ClientOrderIndex should not be less than 1")
	ErrClientOrderIndexTooHigh            error = errors.New("ClientOrderIndex should not be larger than 281474976710655")
	ErrClientOrderIndexDuplicate          error = errors.New("ClientOrderIndex should be unique within the group")
	ErrOrderIndexTooLow                   error = errors.New("OrderIndex should not be less than 281474976710656")
	ErrOrderIndexTooHigh                  error = errors.New("OrderIndex should not be larger than 1152921504606846975")
	ErrBaseAmountTooLow                   error = errors.New("BaseAmount should not be less than 1")
	ErrBaseAmountTooHigh                  error = errors.New("BaseAmount should not be larger than 281474976710655")
	ErrBaseAmountsNotEqual                error = errors.New("BaseAmounts should be equal")
	ErrBaseAmountNotNil                   error = errors.New("BaseAmount should be nil")
	ErrPriceTooLow                        error = errors.New("OrderPrice should not be less than 1")
	ErrPriceTooHigh                       error = errors.New("OrderPrice should not be larger than 4294967295")
	ErrIsAskInvalid                       error = errors.New("IsAsk should be 0 or 1")
	ErrOrderTypeInvalid                   error = errors.New("OrderType is not valid")
	ErrOrderTimeInForceInvalid            error = errors.New("OrderTimeInForce is not valid")
	ErrOrderReduceOnlyInvalid             error = errors.New("ReduceOnly is invalid")
	ErrOrderTriggerPriceInvalid           error = errors.New("TriggerPrice is invalid")
	ErrOrderExpiryInvalid                 error = errors.New("OrderExpiry is invalid")
	ErrGroupingTypeInvalid                error = errors.New("GroupingType is not valid")
	ErrOrderGroupSizeInvalid              error = errors.New("OrderGroupSize is not valid")
	ErrInvalidCancelAllTimeInForce        error = errors.New("CancelAllTimeInForce is invalid")
	ErrCancelAllTimeIsNotInRange          error = errors.New("CancelAllTime should be larger than 0 and not larger than 9223372036854775807")
	ErrCancelAllTimeIsNotNil              error = errors.New("CancelAllTime should be nil")
	ErrCancelAllMarketIndexCantBeSchedule error = errors.New("cancel all for market index can't be scheduled, TimeInforce must be ImmediateCancelAll")
	ErrInvalidMarginMode                  error = errors.New("MarginMode is not valid")
	ErrInitialMarginFractionTooLow        error = errors.New("InitialMarginFraction should not be less than 0")
	ErrInitialMarginFractionTooHigh       error = errors.New("InitialMarginFraction should not be larger than 10000")
	ErrTransferAmountTooLow               error = errors.New("TransferAmount should be larger than 1")
	ErrTransferAmountTooHigh              error = errors.New("TransferAmount should not be larger than 1152921504606846975")
	ErrInvalidUpdateMarginDirection       error = errors.New("margin movement direction is not valid")

	ErrTooManyAttributes                          error = errors.New("too many attributes, should not be larger than 4")
	ErrIntegratorAccountIndexInvalidRange         error = errors.New("IntegratorAccountIndex is in invalid range")
	ErrIntegratorFeeInvalidRange                  error = errors.New("integrator fees are in invalid range")
	ErrIntegratorAccountIndexRequiredForFees      error = errors.New("IntegratorAccountIndex should be non-zero when integrator taker fee or maker fee is non-zero")
	ErrCancelAllMarketIndexInvalidRange           error = errors.New("cancel all for market index attribute is in invalid range")
	ErrSelfTradeBehaviorModeInvalidRange          error = errors.New("SelfTradeBehaviorMode is in invalid range")
	ErrSelfTradeEqualityModeInvalidRange          error = errors.New("SelfTradeEqualityMode is in invalid range")
	ErrSelfTradeSpecificationNotAllowedWithFees   error = errors.New("self-trade specification isn't allowed with integrator fees")
	ErrReduceModeNotAllowedWithMasterEqualityMode error = errors.New("reduce self-trade behavior mode isn't allowed with master account index equality mode")
	ErrOrderVersionInvalidRange                   error = errors.New("OrderOrderVersion is in invalid range")
)
