/*
FILE: internal/orderbook/book.go

DESCRIPTION:
Local order book of one market fed by the order_book WebSocket channel.

PROTOCOL (official WebSocket reference):
  - "subscribed/order_book" carries a full snapshot; every following
    "update/order_book" carries only the changed levels, batched every 50 ms;
    a level with size 0 is removed;
  - each update carries begin_nonce and nonce; continuity holds when
    begin_nonce equals the nonce of the previous update. On a mismatch the
    consumer must resubscribe (the SDK stream does that automatically);
  - offset belongs to the API server and may jump on reconnection — it is not
    used for continuity.

IMPLEMENTATION:
Levels are kept in sorted slices (asks ascending, bids descending) and
updated by binary search + copy. The slices are reused, so a steady state of
updates allocates nothing; a snapshot may grow them. The book is NOT
concurrency-safe: it belongs to the stream goroutine that feeds it, and the
view handed to callbacks is valid only during the callback.

MAIN FUNCTIONS:
  - New(marketID, maxDepth)
  - (Book).ApplySnapshot / ApplyUpdate  : feed the channel pushes.
  - (Book).View / Synced / Reset.
*/

package orderbook

import (
	"sort"
	"time"

	"github.com/tonymontanov/go-lighter/types"
)

// Book — local order book of one market.
type Book struct {
	maxDepth int
	view     types.OrderBook
	synced   bool
}

// New creates an empty book. maxDepth <= 0 keeps every level.
func New(marketID int16, maxDepth int) *Book {
	var b *Book = &Book{maxDepth: maxDepth}
	b.view.MarketID = marketID
	b.view.Asks = make([]types.OrderBookLevel, 0, 256)
	b.view.Bids = make([]types.OrderBookLevel, 0, 256)
	return b
}

// Synced reports whether a snapshot has been applied since the last Reset.
func (b *Book) Synced() bool { return b.synced }

// Reset drops the state (before a resubscribe).
func (b *Book) Reset() {
	b.synced = false
	b.view.Asks = b.view.Asks[:0]
	b.view.Bids = b.view.Bids[:0]
	b.view.Nonce = 0
	b.view.LastUpdatedAtUs = 0
}

// View returns the current book. The slices are owned by the Book.
func (b *Book) View() *types.OrderBook { return &b.view }

// ApplySnapshot replaces the book with the snapshot levels.
func (b *Book) ApplySnapshot(u *types.OrderBookUpdate) {
	b.view.Asks = append(b.view.Asks[:0], u.Asks...)
	b.view.Bids = append(b.view.Bids[:0], u.Bids...)
	sort.Slice(b.view.Asks, func(i, j int) bool { return b.view.Asks[i].Price < b.view.Asks[j].Price })
	sort.Slice(b.view.Bids, func(i, j int) bool { return b.view.Bids[i].Price > b.view.Bids[j].Price })
	b.dropEmpty()
	b.trim()
	b.view.Nonce = u.Nonce
	b.view.LastUpdatedAtUs = u.LastUpdatedAtUs
	b.view.ReceivedAtNs = time.Now().UnixNano()
	b.synced = true
}

// dropEmpty removes zero-size levels of a snapshot (defensive).
func (b *Book) dropEmpty() {
	b.view.Asks = compact(b.view.Asks)
	b.view.Bids = compact(b.view.Bids)
}

// compact removes zero-size levels in place.
func compact(levels []types.OrderBookLevel) []types.OrderBookLevel {
	var kept int
	var i int
	for i = 0; i < len(levels); i++ {
		if levels[i].Size > 0 {
			levels[kept] = levels[i]
			kept++
		}
	}
	return levels[:kept]
}

/*
ApplyUpdate applies a delta. It returns false when the update does not
continue the book (begin_nonce != last nonce, or no snapshot yet); the book
is then left untouched and marked unsynced — the caller must resubscribe.
A snapshot whose nonce was 0 (field absent) accepts the first update
unconditionally.
*/
func (b *Book) ApplyUpdate(u *types.OrderBookUpdate) bool {
	if !b.synced {
		return false
	}
	if b.view.Nonce != 0 && u.BeginNonce != 0 && u.BeginNonce != b.view.Nonce {
		b.synced = false
		return false
	}
	var i int
	for i = 0; i < len(u.Asks); i++ {
		b.view.Asks = applyLevel(b.view.Asks, u.Asks[i], false)
	}
	for i = 0; i < len(u.Bids); i++ {
		b.view.Bids = applyLevel(b.view.Bids, u.Bids[i], true)
	}
	b.trim()
	b.view.Nonce = u.Nonce
	b.view.LastUpdatedAtUs = u.LastUpdatedAtUs
	b.view.ReceivedAtNs = time.Now().UnixNano()
	return true
}

// applyLevel inserts, replaces or removes one level of a sorted side.
func applyLevel(side []types.OrderBookLevel, level types.OrderBookLevel, descending bool) []types.OrderBookLevel {
	var idx int = sort.Search(len(side), func(i int) bool {
		if descending {
			return side[i].Price <= level.Price
		}
		return side[i].Price >= level.Price
	})
	if idx < len(side) && side[idx].Price == level.Price {
		if level.Size <= 0 {
			copy(side[idx:], side[idx+1:])
			return side[:len(side)-1]
		}
		side[idx].Size = level.Size
		return side
	}
	if level.Size <= 0 {
		return side
	}
	side = append(side, types.OrderBookLevel{})
	copy(side[idx+1:], side[idx:])
	side[idx] = level
	return side
}

// trim limits both sides to maxDepth.
func (b *Book) trim() {
	if b.maxDepth <= 0 {
		return
	}
	if len(b.view.Asks) > b.maxDepth {
		b.view.Asks = b.view.Asks[:b.maxDepth]
	}
	if len(b.view.Bids) > b.maxDepth {
		b.view.Bids = b.view.Bids[:b.maxDepth]
	}
}
