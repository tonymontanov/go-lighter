/*
FILE: perpetuals/account.go

DESCRIPTION:
Account, position and margin management of the Perpetuals section. Every
method is the unified function of the common layer plus the section profile.

NOTES:
  - Lighter accounts are one-way (no hedge mode) and cross-margined by
    default; leverage and margin mode are set per market with
    SetLeverage (transaction type 20); isolated margin is adjusted with
    UpdateMargin (type 29).
  - ClosePosition is a recipe (see the method): a reduce-only market order for
    the whole position at a slippage-bounded price.
*/

package perpetuals

import (
	"context"

	"github.com/tonymontanov/go-lighter/internal/domain"
	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/types"
)

// AccountClient — account / position sub-client.
type AccountClient struct {
	c *Client
}

// GetAccount fetches the detailed account (positions of every market,
// assets, collateral).
func (a *AccountClient) GetAccount(ctx context.Context) (types.Account, error) {
	return domain.Account(ctx, a.c.engine(), a.c.prof())
}

// GetPositions returns the positions of the perp markets.
func (a *AccountClient) GetPositions(ctx context.Context) ([]types.Position, error) {
	return domain.Positions(ctx, a.c.engine(), a.c.prof())
}

// GetPosition returns the position of one market (zero size when flat).
func (a *AccountClient) GetPosition(ctx context.Context, symbol string) (types.Position, error) {
	return domain.Position(ctx, a.c.engine(), a.c.prof(), symbol)
}

// GetBalance returns the collateral summary of the account.
func (a *AccountClient) GetBalance(ctx context.Context) (types.Balance, error) {
	return domain.Balance(ctx, a.c.engine(), a.c.prof())
}

// SetLeverage sets the leverage and margin mode of a market (type 20).
func (a *AccountClient) SetLeverage(ctx context.Context, symbol string, leverage int, marginMode types.MarginMode, options types.SendOptions) (types.TxReceipt, error) {
	return domain.UpdateLeverage(ctx, a.c.engine(), a.c.prof(), symbol, leverage, marginMode, options, engine.TransportREST)
}

// UpdateMargin adds (add == true) or removes USDC to / from the isolated
// margin of a position (type 29).
func (a *AccountClient) UpdateMargin(ctx context.Context, symbol string, usdc types.Fixed, add bool, options types.SendOptions) (types.TxReceipt, error) {
	return domain.UpdateMargin(ctx, a.c.engine(), a.c.prof(), symbol, usdc, add, options, engine.TransportREST)
}

/*
ClosePosition closes the position of symbol with a reduce-only market order.
worstPrice bounds the execution (the exchange cancels the unfilled part when
it cannot match at that price or better); use MarketData().SlippagePrice to
derive it from the book. A flat position returns an empty receipt and no
error.
*/
func (a *AccountClient) ClosePosition(ctx context.Context, symbol string, worstPrice types.Fixed, options types.SendOptions) (types.TxReceipt, error) {
	var position types.Position
	var err error
	position, err = a.GetPosition(ctx, symbol)
	if err != nil {
		return types.TxReceipt{}, err
	}
	if position.IsFlat() {
		return types.TxReceipt{}, nil
	}
	return a.c.trading.CreateOrder(ctx, types.CreateOrderRequest{
		Symbol:      symbol,
		IsAsk:       position.Sign > 0,
		Price:       worstPrice,
		Size:        position.Position,
		Type:        types.OrderTypeMarket,
		TimeInForce: types.TimeInForceIOC,
		ReduceOnly:  true,
	}, options)
}

// GetAPIKeys lists the registered API keys of the account (apiKeyIndex 255 = all).
func (a *AccountClient) GetAPIKeys(ctx context.Context, apiKeyIndex uint8) ([]types.APIKey, error) {
	return domain.APIKeys(ctx, a.c.engine(), apiKeyIndex)
}

/*
CheckAPIKey verifies that the configured signing key matches the public key
registered on the exchange for its index — the equivalent of lighter-go's
CheckClient. Run it once at start-up: a mismatch means every transaction
would be rejected with "invalid signature".
*/
func (a *AccountClient) CheckAPIKey(ctx context.Context, apiKeyIndex uint8) error {
	if apiKeyIndex == 0 {
		apiKeyIndex = a.c.engine().DefaultAPIKeyIndex()
	}
	var local string = a.c.engine().PublicKeyHex(apiKeyIndex)
	if local == "" {
		return a.c.prof().Invalid("CheckAPIKey", "api key index is not configured")
	}
	var keys []types.APIKey
	var err error
	keys, err = a.GetAPIKeys(ctx, apiKeyIndex)
	if err != nil {
		return err
	}
	var i int
	for i = 0; i < len(keys); i++ {
		if keys[i].APIKeyIndex == apiKeyIndex {
			if keys[i].PublicKey == local {
				return nil
			}
			return a.c.prof().Invalid("CheckAPIKey", "the configured private key does not match the public key registered on the exchange")
		}
	}
	return a.c.prof().Invalid("CheckAPIKey", "the exchange has no key registered at this index")
}

// GetAccountLimits fetches the tier and fee ticks of the account.
func (a *AccountClient) GetAccountLimits(ctx context.Context) (types.AccountLimits, error) {
	return domain.AccountLimits(ctx, a.c.engine())
}

// GetTrades returns the latest trades of the account on one market.
func (a *AccountClient) GetTrades(ctx context.Context, symbol string, limit int) ([]types.Trade, error) {
	return domain.AccountTrades(ctx, a.c.engine(), a.c.prof(), symbol, limit)
}
