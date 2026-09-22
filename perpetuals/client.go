/*
FILE: perpetuals/client.go

DESCRIPTION:
Section client of Lighter Perpetuals (market_type "perp"). Holds the section
Profile (the only section-specific input of the unified functions) and the
four domain sub-clients.

SECTION SPECIFICS DEFINED HERE:
  - MarketType = "perp": the orderBookDetails filter of the market loader;
  - account-wide answers (orders, positions) are filtered to perp markets.

MAIN FUNCTIONS:
  - NewClient(parent)   : section constructor; no network I/O. Market
                          metadata is loaded on first use and then refreshed
                          every Config.MarketRefreshInterval.
  - Trading / Account / MarketData / Stream : sub-clients.
  - RefreshMarkets(ctx) : force a metadata reload (e.g. after a listing).
*/

package perpetuals

import (
	"context"

	lighter "github.com/tonymontanov/go-lighter"
	"github.com/tonymontanov/go-lighter/internal/domain"
	"github.com/tonymontanov/go-lighter/internal/engine"
	"github.com/tonymontanov/go-lighter/internal/markets"
	"github.com/tonymontanov/go-lighter/types"
)

const (
	// SectionName — name of the section (exchange vocabulary: perpetual markets).
	SectionName string = "perpetuals"
	// MarketType — market_type of the section.
	MarketType types.MarketType = types.MarketTypePerp
)

// Client — Perpetuals section client. Safe for concurrent use.
type Client struct {
	parent   *lighter.Client
	profile  domain.Profile
	registry *markets.Registry

	trading *TradingClient
	account *AccountClient
	market  *MarketDataClient
	stream  *StreamClient
}

// NewClient builds the section on top of the root client.
func NewClient(parent *lighter.Client) *Client {
	var c *Client = &Client{parent: parent}
	c.registry = markets.NewRegistry(c.loadMarkets)
	c.profile = domain.Profile{Section: SectionName, MarketType: MarketType, Registry: c.registry}

	c.trading = &TradingClient{c: c, transport: engine.TransportREST}
	c.account = &AccountClient{c: c}
	c.market = &MarketDataClient{c: c}
	c.stream = &StreamClient{c: c}

	var cfg lighter.Config = parent.Config()
	c.registry.StartAutoRefresh(parent.LifeContext(), cfg.MarketRefreshInterval, cfg.Logger,
		cfg.Metrics.Counter("lighter_market_registry_refresh_total", "section", SectionName))
	return c
}

// Trading returns the order management sub-client (REST transport).
func (c *Client) Trading() *TradingClient { return c.trading }

// Account returns the account / position sub-client.
func (c *Client) Account() *AccountClient { return c.account }

// MarketData returns the market data sub-client.
func (c *Client) MarketData() *MarketDataClient { return c.market }

// Stream returns the WebSocket streams sub-client.
func (c *Client) Stream() *StreamClient { return c.stream }

// RefreshMarkets reloads market metadata immediately.
func (c *Client) RefreshMarkets(ctx context.Context) error {
	return c.registry.Refresh(ctx)
}

// engine / prof / logger — private shortcuts of the sub-clients.
func (c *Client) engine() *engine.Engine { return c.parent.Engine() }
func (c *Client) prof() *domain.Profile  { return &c.profile }
func (c *Client) logger() lighter.Logger { return c.parent.Logger() }

// loadMarkets is the section's metadata loader: orderBookDetails?filter=perp.
func (c *Client) loadMarkets(ctx context.Context) ([]types.MarketInfo, error) {
	return domain.LoadMarkets(ctx, c.engine(), &c.profile)
}

// streamDeps builds the dependencies of the stream functions.
func (c *Client) streamDeps() domain.StreamDeps {
	return domain.StreamDeps{Conn: c.parent.StreamConn(), Engine: c.engine(), Logger: c.logger()}
}
