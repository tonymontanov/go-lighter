/*
FILE: metrics.go

DESCRIPTION:
Public re-export of the SDK metrics contract (implementation: internal/ltmet).
Counter / CounterFactory are shaped like prometheus counters but are not tied
to any backend. See internal/ltmet for the list of counter names.

The default factory is a no-op.
*/

package lighter

import "github.com/tonymontanov/go-lighter/internal/ltmet"

// Counter — a single monotonically increasing counter.
type Counter = ltmet.Counter

// CounterFactory — counter factory (name + label pairs).
type CounterFactory = ltmet.CounterFactory

// NoopMetrics returns the no-op counter factory.
func NoopMetrics() CounterFactory { return ltmet.Noop() }
