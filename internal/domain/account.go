/*
FILE: internal/domain/account.go

DESCRIPTION:
Unified account reads of the common layer.

ENDPOINTS (openapi.json, verified live on 2026-09-22):
  GET account?by=index&value=N                     → accounts[] (DetailedAccount)
  GET accountActiveOrders?account_index=N[&market_id=M]   (auth) → orders[]
  GET accountInactiveOrders?account_index=N&limit=L[&market_id=M][&cursor=C] (auth) → orders[], next_cursor
  GET accountOrders?account_index=N&client_order_indexes=[..] (auth) → orders[]
  GET apikeys?account_index=N&api_key_index=K (255 = all) → api_keys[]
  GET accountLimits?account_index=N (auth)         → tier and fee ticks
  GET trades?account_index=N&market_id=M&sort_by=timestamp&limit=L (auth) → trades[]

Private reads attach the auth token in the "authorization" header; a
missing token answers code 20001 ("auth query param and Authorization
header are empty" — observed live).

Account-wide answers (orders, positions) mix every market of the account;
a section keeps only the markets of its own registry.
*/

package domain

import (
	"context"
	"strconv"

	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/internal/ratelimit"
	"github.com/tonymontanov/go-lighter/internal/rest"
	"github.com/tonymontanov/go-lighter/types"
)

// accountsResponse — account endpoint envelope (DetailedAccounts).
type accountsResponse struct {
	Accounts []types.Account `json:"accounts"`
}

// Account fetches the detailed account (positions, assets, collateral).
func Account(ctx context.Context, e *engine.Engine, p *Profile) (types.Account, error) {
	const operation string = "Account"
	var q rest.Query
	q.Str("by", "index").Int("value", e.AccountIndex())
	var resp accountsResponse
	var err error = e.Query(ctx, "account", q.String(), false, &resp, ratelimit.CategoryQuery)
	if err != nil {
		return types.Account{}, err
	}
	if len(resp.Accounts) == 0 {
		return types.Account{}, p.invalid(operation, "account "+strconv.FormatInt(e.AccountIndex(), 10)+" not found")
	}
	return resp.Accounts[0], nil
}

// Positions returns the positions of the section's markets (open or flat).
func Positions(ctx context.Context, e *engine.Engine, p *Profile) ([]types.Position, error) {
	var err error = ensureMarkets(ctx, p)
	if err != nil {
		return nil, err
	}
	var account types.Account
	account, err = Account(ctx, e, p)
	if err != nil {
		return nil, err
	}
	var out []types.Position = account.Positions[:0]
	var i int
	for i = 0; i < len(account.Positions); i++ {
		if p.Owns(account.Positions[i].MarketID) {
			out = append(out, account.Positions[i])
		}
	}
	return out, nil
}

// Position returns the position of one market (a zero-size Position when
// the account has none).
func Position(ctx context.Context, e *engine.Engine, p *Profile, symbol string) (types.Position, error) {
	const operation string = "Position"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return types.Position{}, err
	}
	var account types.Account
	account, err = Account(ctx, e, p)
	if err != nil {
		return types.Position{}, err
	}
	var found *types.Position = account.PositionOf(info.MarketID)
	if found == nil {
		return types.Position{MarketID: info.MarketID, Symbol: info.Symbol}, nil
	}
	return *found, nil
}

// Balance returns the collateral summary of the account.
func Balance(ctx context.Context, e *engine.Engine, p *Profile) (types.Balance, error) {
	var account types.Account
	var err error
	account, err = Account(ctx, e, p)
	if err != nil {
		return types.Balance{}, err
	}
	return types.Balance{
		Collateral:       account.Collateral,
		AvailableBalance: account.AvailableBalance,
		TotalAssetValue:  account.TotalAssetValue,
		CrossAssetValue:  account.CrossAssetValue,
	}, nil
}

// ensureMarkets loads the registry (network I/O on first use only).
func ensureMarkets(ctx context.Context, p *Profile) error {
	var err error
	_, err = p.Registry.Get(ctx)
	return err
}

// ordersResponse — orders envelope.
type ordersResponse struct {
	NextCursor string        `json:"next_cursor"`
	Orders     []types.Order `json:"orders"`
}

// filterOwned keeps the orders of the section's markets (in place).
func (p *Profile) filterOwned(orders []types.Order) []types.Order {
	var out []types.Order = orders[:0]
	var i int
	for i = 0; i < len(orders); i++ {
		if p.Owns(orders[i].MarketIndex) {
			out = append(out, orders[i])
		}
	}
	return out
}

/*
ActiveOrders returns the resting / pending orders of the account. symbol ==
"" returns every order of the section (the answer of the exchange is
account-wide; other sections' markets are filtered out).
*/
func ActiveOrders(ctx context.Context, e *engine.Engine, p *Profile, symbol string) ([]types.Order, error) {
	const operation string = "ActiveOrders"
	var err error = ensureMarkets(ctx, p)
	if err != nil {
		return nil, err
	}
	var q rest.Query
	q.Int("account_index", e.AccountIndex())
	if symbol != "" {
		var info *types.MarketInfo
		info, err = p.resolve(ctx, operation, symbol)
		if err != nil {
			return nil, err
		}
		q.Int("market_id", int64(info.MarketID))
	} else {
		q.Str("market_type", string(p.MarketType))
	}
	var resp ordersResponse
	err = e.Query(ctx, "accountActiveOrders", q.String(), true, &resp, ratelimit.CategoryQuery)
	if err != nil {
		return nil, err
	}
	return p.filterOwned(resp.Orders), nil
}

// InactiveOrdersPage — one page of inactive (filled / cancelled) orders.
type InactiveOrdersPage struct {
	Orders     []types.Order
	NextCursor string
}

/*
InactiveOrders returns a page of the account's inactive orders, newest
first. symbol == "" returns every market of the section; cursor == "" starts
from the newest; limit is required by the exchange (1..).
*/
func InactiveOrders(ctx context.Context, e *engine.Engine, p *Profile, symbol string, limit int, cursor string) (InactiveOrdersPage, error) {
	const operation string = "InactiveOrders"
	var page InactiveOrdersPage
	var err error = ensureMarkets(ctx, p)
	if err != nil {
		return page, err
	}
	if limit <= 0 {
		limit = 100
	}
	var q rest.Query
	q.Int("account_index", e.AccountIndex()).Int("limit", int64(limit))
	if symbol != "" {
		var info *types.MarketInfo
		info, err = p.resolve(ctx, operation, symbol)
		if err != nil {
			return page, err
		}
		q.Int("market_id", int64(info.MarketID))
	} else {
		q.Str("market_type", string(p.MarketType))
	}
	if cursor != "" {
		if !safeText(cursor) {
			return page, p.invalid(operation, "malformed cursor")
		}
		q.Str("cursor", cursor)
	}
	var resp ordersResponse
	err = e.Query(ctx, "accountInactiveOrders", q.String(), true, &resp, ratelimit.CategoryQuery)
	if err != nil {
		return page, err
	}
	page.Orders = p.filterOwned(resp.Orders)
	page.NextCursor = resp.NextCursor
	return page, nil
}

// OrdersByClientIndex fetches orders by client order indexes (accountOrders).
func OrdersByClientIndex(ctx context.Context, e *engine.Engine, p *Profile, clientOrderIndexes []int64) ([]types.Order, error) {
	const operation string = "OrdersByClientIndex"
	if len(clientOrderIndexes) == 0 {
		return nil, p.invalid(operation, "no client order indexes")
	}
	var err error = ensureMarkets(ctx, p)
	if err != nil {
		return nil, err
	}
	var list []byte = make([]byte, 0, 16*len(clientOrderIndexes))
	list = append(list, '[')
	var i int
	for i = 0; i < len(clientOrderIndexes); i++ {
		if i > 0 {
			list = append(list, ',')
		}
		list = strconv.AppendInt(list, clientOrderIndexes[i], 10)
	}
	list = append(list, ']')
	var q rest.Query
	q.Int("account_index", e.AccountIndex()).Str("client_order_indexes", string(list))
	var resp ordersResponse
	err = e.Query(ctx, "accountOrders", q.String(), true, &resp, ratelimit.CategoryQuery)
	if err != nil {
		return nil, err
	}
	return p.filterOwned(resp.Orders), nil
}

// apiKeysResponse — apikeys envelope.
type apiKeysResponse struct {
	APIKeys []types.APIKey `json:"api_keys"`
}

// APIKeys lists the registered API keys of the account (apiKeyIndex 255 = all).
func APIKeys(ctx context.Context, e *engine.Engine, apiKeyIndex uint8) ([]types.APIKey, error) {
	var q rest.Query
	q.Int("account_index", e.AccountIndex()).Int("api_key_index", int64(apiKeyIndex))
	var resp apiKeysResponse
	var err error = e.Query(ctx, "apikeys", q.String(), false, &resp, ratelimit.CategoryQuery)
	if err != nil {
		return nil, err
	}
	return resp.APIKeys, nil
}

// AccountLimits fetches the tier and fee ticks of the account (auth).
func AccountLimits(ctx context.Context, e *engine.Engine) (types.AccountLimits, error) {
	var q rest.Query
	q.Int("account_index", e.AccountIndex())
	var out types.AccountLimits
	var err error = e.Query(ctx, "accountLimits", q.String(), true, &out, ratelimit.CategoryQuery)
	return out, err
}

/*
AccountTrades returns the latest trades of the account on one market
(trades endpoint, sorted by timestamp descending). limit is required by the
exchange.
*/
func AccountTrades(ctx context.Context, e *engine.Engine, p *Profile, symbol string, limit int) ([]types.Trade, error) {
	const operation string = "AccountTrades"
	var info *types.MarketInfo
	var err error
	info, err = p.resolve(ctx, operation, symbol)
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	var q rest.Query
	q.Int("account_index", e.AccountIndex()).Int("market_id", int64(info.MarketID)).Str("sort_by", "timestamp").Int("limit", int64(limit))
	var resp tradesResponse
	err = e.Query(ctx, "trades", q.String(), true, &resp, ratelimit.CategoryQuery)
	if err != nil {
		return nil, err
	}
	return resp.Trades, nil
}
