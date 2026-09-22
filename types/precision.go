/*
FILE: types/precision.go

DESCRIPTION:
Price and size grid of a Lighter market, implemented on top of Fixed in pure
int64 arithmetic (no allocations, no floating point).

EXCHANGE RULES (official "Signing Transactions" page, orderBookDetails):
  - Every market publishes supported_price_decimals and
    supported_size_decimals. A price is transmitted as the integer
    price * 10^supported_price_decimals (uint32 on the wire, 1..2^32-1); a
    size as size * 10^supported_size_decimals (int64, 1..2^48-1).
  - min_base_amount / min_quote_amount are the minimums of a MAKER order (the
    larger of the two applies); they are exposed in MarketInfo and not
    enforced by the SDK, because the exchange exempts taker orders.
The grid is therefore static per market (unlike Hyperliquid's significant
figure rule): tick = 10^-price_decimals, lot = 10^-size_decimals.

MAIN FUNCTIONS:
  - (Precision).TickSize / LotSize
  - (Precision).ValidatePrice / NormalizePrice / WirePrice / PriceFromWire
  - (Precision).ValidateSize  / NormalizeSize  / WireSize  / SizeFromWire
  - MulFixed : overflow-safe Fixed multiplication (notional = price * size).
*/

package types

import "math/bits"

// RoundingMode — direction used when a value has to be snapped to a grid.
type RoundingMode uint8

const (
	// RoundNearest — to the nearest grid point, ties away from zero.
	RoundNearest RoundingMode = iota
	// RoundDown — toward zero (floor for positive values). Use for bid prices
	// and for sizes.
	RoundDown
	// RoundUp — away from zero (ceil for positive values). Use for ask prices.
	RoundUp
)

// Wire limits of an order (lighter-go types/txtypes/constants.go).
const (
	// MinWirePrice — smallest valid wire price.
	MinWirePrice uint32 = 1
	// MaxWirePrice — largest valid wire price (2^32 - 1).
	MaxWirePrice uint32 = 1<<32 - 1
	// MinWireSize — smallest valid wire base amount.
	MinWireSize int64 = 1
	// MaxWireSize — largest valid wire base amount (2^48 - 1).
	MaxWireSize int64 = 1<<48 - 1
)

// pow10 — powers of ten up to 10^8.
var pow10 [9]int64 = [9]int64{1, 10, 100, 1_000, 10_000, 100_000, 1_000_000, 10_000_000, 100_000_000}

// Precision — grid parameters of one market.
type Precision struct {
	// PriceDecimals — supported_price_decimals of the market (0..8).
	PriceDecimals int
	// SizeDecimals — supported_size_decimals of the market (0..8).
	SizeDecimals int
}

// clampDecimals clamps d into [0, FixedDecimals].
func clampDecimals(d int) int {
	if d < 0 {
		return 0
	}
	if d > FixedDecimals {
		return FixedDecimals
	}
	return d
}

// roundToStep snaps a non-negative raw value to a multiple of step.
func roundToStep(v int64, step int64, mode RoundingMode) int64 {
	var rem int64 = v % step
	if rem == 0 {
		return v
	}
	var down int64 = v - rem
	switch mode {
	case RoundDown:
		return down
	case RoundUp:
		return down + step
	default:
		if rem*2 >= step {
			return down + step
		}
		return down
	}
}

// Valid reports whether both decimal counts can be represented by Fixed.
func (p Precision) Valid() bool {
	return p.PriceDecimals >= 0 && p.PriceDecimals <= FixedDecimals &&
		p.SizeDecimals >= 0 && p.SizeDecimals <= FixedDecimals
}

// TickSize returns the price grid step (10^-PriceDecimals).
func (p Precision) TickSize() Fixed {
	return Fixed(pow10[FixedDecimals-clampDecimals(p.PriceDecimals)])
}

// LotSize returns the size grid step (10^-SizeDecimals).
func (p Precision) LotSize() Fixed {
	return Fixed(pow10[FixedDecimals-clampDecimals(p.SizeDecimals)])
}

// ValidatePrice reports whether px can be sent to the exchange as is: positive,
// on the tick grid and inside the uint32 wire range.
func (p Precision) ValidatePrice(px Fixed) bool {
	var ok bool
	_, ok = p.WirePrice(px)
	return ok
}

// NormalizePrice snaps px to the tick grid in the given direction.
// Non-positive input is returned unchanged.
func (p Precision) NormalizePrice(px Fixed, mode RoundingMode) Fixed {
	if px <= 0 {
		return px
	}
	return Fixed(roundToStep(int64(px), int64(p.TickSize()), mode))
}

// WirePrice converts px to the wire integer. ok is false when px is not
// positive, is off the grid or does not fit into the wire range.
func (p Precision) WirePrice(px Fixed) (uint32, bool) {
	if px <= 0 {
		return 0, false
	}
	var tick int64 = int64(p.TickSize())
	var v int64 = int64(px)
	if v%tick != 0 {
		return 0, false
	}
	var wire int64 = v / tick
	if wire < int64(MinWirePrice) || wire > int64(MaxWirePrice) {
		return 0, false
	}
	return uint32(wire), true
}

// PriceFromWire converts a wire price back to Fixed.
func (p Precision) PriceFromWire(wire uint32) Fixed {
	return Fixed(int64(wire) * int64(p.TickSize()))
}

// ValidateSize reports whether sz can be sent to the exchange as is: positive,
// on the lot grid and inside the int64 (48-bit) wire range.
func (p Precision) ValidateSize(sz Fixed) bool {
	var ok bool
	_, ok = p.WireSize(sz)
	return ok
}

// NormalizeSize snaps sz to the lot grid. RoundDown is the safe default for
// sizes (never exceeds the requested quantity). Non-positive input is
// returned unchanged.
func (p Precision) NormalizeSize(sz Fixed, mode RoundingMode) Fixed {
	if sz <= 0 {
		return sz
	}
	return Fixed(roundToStep(int64(sz), int64(p.LotSize()), mode))
}

// WireSize converts sz to the wire integer. ok is false when sz is not
// positive, is off the grid or does not fit into the wire range.
func (p Precision) WireSize(sz Fixed) (int64, bool) {
	if sz <= 0 {
		return 0, false
	}
	var lot int64 = int64(p.LotSize())
	var v int64 = int64(sz)
	if v%lot != 0 {
		return 0, false
	}
	var wire int64 = v / lot
	if wire < MinWireSize || wire > MaxWireSize {
		return 0, false
	}
	return wire, true
}

// SizeFromWire converts a wire base amount back to Fixed.
func (p Precision) SizeFromWire(wire int64) Fixed {
	return Fixed(wire * int64(p.LotSize()))
}

/*
MulFixed multiplies two Fixed values with a 128-bit intermediate, truncating
the result toward zero to 8 fractional digits. ok is false on overflow.

Typical use: notional = MulFixed(price, size) compared against MinQuoteAmount.
*/
func MulFixed(a Fixed, b Fixed) (Fixed, bool) {
	var negative bool = (a < 0) != (b < 0)
	var hi uint64
	var lo uint64
	hi, lo = bits.Mul64(uint64(a.Abs()), uint64(b.Abs()))
	if hi >= uint64(FixedScale) {
		return 0, false
	}
	var quotient uint64
	quotient, _ = bits.Div64(hi, lo, uint64(FixedScale))
	if quotient > uint64(1<<63-1) {
		return 0, false
	}
	var result Fixed = Fixed(int64(quotient))
	if negative {
		result = -result
	}
	return result, true
}
