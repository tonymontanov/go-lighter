/*
FILE: internal/domain/profile.go

DESCRIPTION:
Package domain holds the UNIFIED domain functions of the common layer:
placing, modifying and cancelling orders, the account and market-data reads
and the WebSocket streams that are identical for every section of the
exchange.

THE TWO-LAYER RULE, IN CODE:

	// common layer (this package) — one implementation for the whole exchange
	func CreateOrder(ctx, e, profile, request, options) (types.TxReceipt, error)

	// section layer — the unified function with the section's specifics
	func (t *TradingClient) CreateOrder(ctx, request, options) (types.TxReceipt, error) {
		return domain.CreateOrder(ctx, t.c.engine(), t.c.profile(), request, options)
	}

A section never calls another section; everything two sections could share
lives here and is parameterised by Profile — the ONLY thing a section brings:
  - Section    : name used in error messages ("perpetuals", "spot", ...);
  - MarketType : market_type filter of the metadata request ("perp", "spot");
  - Registry   : symbol → market id / precision, built from orderBookDetails.

MAIN ENTITIES:
  - Profile          : section parameters.
  - invalid          : InvalidRequest error constructor with the house message
                       format "<section>.<Operation>: <detail>".
  - resolve          : symbol → *types.MarketInfo through the registry.
*/

package domain

import (
	"context"

	"github.com/tonymontanov/go-lighter/internal/lterr"
	"github.com/tonymontanov/go-lighter/internal/markets"
	"github.com/tonymontanov/go-lighter/types"
)

// Profile — everything a section contributes to the unified functions.
type Profile struct {
	// Section — section name for error messages.
	Section string
	// MarketType — market_type of the section ("perp" / "spot").
	MarketType types.MarketType
	// Registry — market registry of the section.
	Registry *markets.Registry
}

// invalid builds an InvalidRequest error: "<section>.<operation>: <detail>".
func (p *Profile) invalid(operation string, detail string) *lterr.Error {
	return lterr.New(lterr.ErrorKindInvalidRequest, p.Section+"."+operation+": "+detail, nil)
}

// Invalid is the exported form of invalid for validation done by sections.
func (p *Profile) Invalid(operation string, detail string) error {
	return p.invalid(operation, detail)
}

// Resolve returns the market of a symbol, loading metadata on first use.
func (p *Profile) Resolve(ctx context.Context, operation string, symbol string) (*types.MarketInfo, error) {
	return p.resolve(ctx, operation, symbol)
}

// resolve returns the market of a symbol, loading metadata on first use.
func (p *Profile) resolve(ctx context.Context, operation string, symbol string) (*types.MarketInfo, error) {
	if symbol == "" {
		return nil, p.invalid(operation, "symbol is empty")
	}
	var snapshot *markets.Snapshot
	var err error
	snapshot, err = p.Registry.Get(ctx)
	if err != nil {
		return nil, err
	}
	var info *types.MarketInfo
	var ok bool
	info, ok = snapshot.BySymbol(symbol)
	if !ok {
		return nil, p.invalid(operation, "unknown symbol "+symbol)
	}
	return info, nil
}

// Owns reports whether a market id belongs to the section. Never performs
// I/O — safe on the WS read goroutine; callers load the registry first.
func (p *Profile) Owns(marketID int16) bool {
	var snapshot *markets.Snapshot = p.Registry.Peek()
	if snapshot == nil {
		return false
	}
	var ok bool
	_, ok = snapshot.ByID(marketID)
	return ok
}

// MarketByID returns the market of an id from the loaded registry (nil when
// unknown or not loaded). Never performs I/O.
func (p *Profile) MarketByID(marketID int16) *types.MarketInfo {
	var snapshot *markets.Snapshot = p.Registry.Peek()
	if snapshot == nil {
		return nil
	}
	var info *types.MarketInfo
	var ok bool
	info, ok = snapshot.ByID(marketID)
	if !ok {
		return nil
	}
	return info
}

// safeText reports whether s can be embedded into a query string or a JSON
// string without escaping. Symbols and resolutions always can; anything else
// is a caller bug and is rejected instead of being escaped.
func safeText(s string) bool {
	var i int
	for i = 0; i < len(s); i++ {
		var c byte = s[i]
		if c < 0x20 || c == '"' || c == '\\' || c == '&' || c == '=' || c == '#' || c == '%' || c >= 0x7f {
			return false
		}
	}
	return true
}
