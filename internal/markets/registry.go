/*
FILE: internal/markets/registry.go

DESCRIPTION:
Market registry of the common layer: symbol ↔ market id ↔ precision.

WHY IT EXISTS:
Transactions and channels address markets by a numeric id that comes from
exchange metadata (orderBookDetails), differs between mainnet and testnet and
grows as markets are listed (235 perp markets on mainnet on 2026-09-22, ids up
to 4095). The mapping therefore cannot be hard-coded: it is built from
metadata on first use and refreshed in the background.

The registry itself is SECTION-AGNOSTIC. It stores an immutable Snapshot
behind an atomic pointer; the section supplies the Loader that knows which
market_type filter to request. Lookups on the order hot path are a single
atomic load plus a map read — no locks, no allocations.

MAIN FUNCTIONS:
  - NewRegistry(loader)          : lazy registry.
  - (Registry).Get(ctx)          : current snapshot, loading it on first use
                                   (concurrent first callers share one load).
  - (Registry).Refresh(ctx)      : reload and atomically swap.
  - (Registry).StartAutoRefresh  : background refresh until ctx is done.
  - (Snapshot).BySymbol / ByID / All.
*/

package markets

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tonymontanov/go-lighter/internal/ltlog"
	"github.com/tonymontanov/go-lighter/internal/ltmet"
	"github.com/tonymontanov/go-lighter/types"
)

// Loader fetches exchange metadata and converts it into market descriptions.
type Loader func(ctx context.Context) ([]types.MarketInfo, error)

// Snapshot — immutable view of the markets of one section.
type Snapshot struct {
	list       []types.MarketInfo
	bySymbol   map[string]*types.MarketInfo
	byID       map[int16]*types.MarketInfo
	loadedAtMs int64
}

// NewSnapshot indexes a list of markets. The list is owned by the snapshot.
func NewSnapshot(list []types.MarketInfo, loadedAtMs int64) *Snapshot {
	var s *Snapshot = &Snapshot{
		list:       list,
		bySymbol:   make(map[string]*types.MarketInfo, len(list)),
		byID:       make(map[int16]*types.MarketInfo, len(list)),
		loadedAtMs: loadedAtMs,
	}
	var i int
	for i = 0; i < len(list); i++ {
		s.bySymbol[list[i].Symbol] = &list[i]
		s.byID[list[i].MarketID] = &list[i]
	}
	return s
}

// BySymbol looks a market up by its exchange symbol (case-sensitive).
func (s *Snapshot) BySymbol(symbol string) (*types.MarketInfo, bool) {
	var info *types.MarketInfo
	var ok bool
	info, ok = s.bySymbol[symbol]
	return info, ok
}

// ByID looks a market up by its market id.
func (s *Snapshot) ByID(id int16) (*types.MarketInfo, bool) {
	var info *types.MarketInfo
	var ok bool
	info, ok = s.byID[id]
	return info, ok
}

// All returns every market of the snapshot. The slice must not be modified.
func (s *Snapshot) All() []types.MarketInfo { return s.list }

// LoadedAtMs returns the unix ms timestamp of the load.
func (s *Snapshot) LoadedAtMs() int64 { return s.loadedAtMs }

// Registry — lazily loaded, atomically swappable market snapshot.
type Registry struct {
	loader  Loader
	current atomic.Pointer[Snapshot]
	// loadMu serialises loads so concurrent first callers share one request.
	loadMu sync.Mutex
}

// NewRegistry creates a registry. No I/O happens until the first Get / Refresh.
func NewRegistry(loader Loader) *Registry {
	return &Registry{loader: loader}
}

// Peek returns the current snapshot or nil when nothing is loaded yet. Never
// performs I/O — safe for hot paths that must not block.
func (r *Registry) Peek() *Snapshot {
	return r.current.Load()
}

// Get returns the current snapshot, loading it on first use.
func (r *Registry) Get(ctx context.Context) (*Snapshot, error) {
	var snapshot *Snapshot = r.current.Load()
	if snapshot != nil {
		return snapshot, nil
	}
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	snapshot = r.current.Load()
	if snapshot != nil {
		return snapshot, nil
	}
	return r.loadLocked(ctx)
}

// Refresh reloads metadata and swaps the snapshot. On failure the previous
// snapshot stays in place.
func (r *Registry) Refresh(ctx context.Context) error {
	r.loadMu.Lock()
	defer r.loadMu.Unlock()
	var err error
	_, err = r.loadLocked(ctx)
	return err
}

// loadLocked performs one load. Caller holds loadMu.
func (r *Registry) loadLocked(ctx context.Context) (*Snapshot, error) {
	var list []types.MarketInfo
	var err error
	list, err = r.loader(ctx)
	if err != nil {
		return nil, err
	}
	var snapshot *Snapshot = NewSnapshot(list, time.Now().UnixMilli())
	r.current.Store(snapshot)
	return snapshot, nil
}

// StartAutoRefresh refreshes the registry every interval until ctx is done.
// interval <= 0 disables the loop. Failures are logged and retried on the next
// tick — the previous snapshot keeps serving lookups.
func (r *Registry) StartAutoRefresh(ctx context.Context, interval time.Duration, logger ltlog.Logger, refreshed ltmet.Counter) {
	if interval <= 0 {
		return
	}
	go func() {
		var ticker *time.Ticker = time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				var err error = r.Refresh(ctx)
				if err != nil {
					if ctx.Err() == nil {
						logger.Warn("markets: metadata refresh failed", ltlog.Err(err))
					}
					continue
				}
				refreshed.Inc()
			}
		}
	}()
}
