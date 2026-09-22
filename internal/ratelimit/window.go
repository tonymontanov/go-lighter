/*
FILE: internal/ratelimit/window.go

DESCRIPTION:
Lock-free sliding one-minute window of consumed budget.

The window is a ring of 60 one-second buckets. Each bucket is ONE
atomic.Uint64 packing (unix second << 32 | amount), so "the bucket belongs to
an older second → reset it" and "add amount" happen in a single CAS and no
update can be lost. Add and Used never block and never allocate.

Lighter publishes its limits per ROLLING minute and sends no rate-limit
headers, so the SDK accounts for every request it sends on its own side; see
limiter.go for the buckets built on top of this window.

MAIN FUNCTIONS:
  - NewWindow(limit) / (Window).Add / Used / Remaining / Limit
*/

package ratelimit

import (
	"sync/atomic"
	"time"
)

// windowSeconds — length of the sliding window.
const windowSeconds int64 = 60

// Window — sliding one-minute budget window. Safe for concurrent use.
type Window struct {
	limit   int64
	buckets [windowSeconds]atomic.Uint64
	// now — clock in unix seconds, replaceable in tests.
	now func() int64
}

// wallClockSeconds returns the current unix time in seconds.
func wallClockSeconds() int64 {
	return time.Now().Unix()
}

// NewWindow creates a window with the given per-minute limit.
func NewWindow(limit int64) *Window {
	return &Window{limit: limit, now: wallClockSeconds}
}

// NewWindowWithClock creates a window with a custom clock (tests).
func NewWindowWithClock(limit int64, now func() int64) *Window {
	return &Window{limit: limit, now: now}
}

// Limit returns the configured per-minute limit.
func (w *Window) Limit() int64 { return w.limit }

// Add records amount consumed now and returns the amount used within the
// window after the addition.
func (w *Window) Add(amount int64) int64 {
	var second int64 = w.now()
	if amount > 0 {
		var bucket *atomic.Uint64 = &w.buckets[second%windowSeconds]
		for {
			var old uint64 = bucket.Load()
			var accumulated uint64
			if int64(old>>32) == second&0xffffffff {
				accumulated = old & 0xffffffff
			}
			accumulated += uint64(amount)
			if accumulated > 0xffffffff {
				accumulated = 0xffffffff
			}
			var updated uint64 = uint64(second&0xffffffff)<<32 | accumulated
			if bucket.CompareAndSwap(old, updated) {
				break
			}
		}
	}
	return w.usedAt(second)
}

// Used returns the amount consumed within the last 60 seconds.
func (w *Window) Used() int64 {
	return w.usedAt(w.now())
}

// Remaining returns limit - Used(), never negative.
func (w *Window) Remaining() int64 {
	var remaining int64 = w.limit - w.Used()
	if remaining < 0 {
		return 0
	}
	return remaining
}

// usedAt sums the buckets that belong to (second-60, second].
func (w *Window) usedAt(second int64) int64 {
	var total int64
	var i int64
	for i = 0; i < windowSeconds; i++ {
		var packed uint64 = w.buckets[i].Load()
		var stamp int64 = int64(packed >> 32)
		var age int64 = (second & 0xffffffff) - stamp
		if age >= 0 && age < windowSeconds {
			total += int64(packed & 0xffffffff)
		}
	}
	return total
}
