/*
FILE: logger.go

DESCRIPTION:
Public re-export of the SDK logging contract (implementation: internal/ltlog).
The SDK depends on no logging library: the application adapts its own logger
(zerolog, zap, slog) to the 4-method Logger interface. Fields are typed values,
not ...any, so logging never boxes or allocates on hot paths.

The default logger is a no-op.
*/

package lighter

import "github.com/tonymontanov/go-lighter/internal/ltlog"

// Logger — minimal SDK logging contract.
type Logger = ltlog.Logger

// Field — typed key-value log field.
type Field = ltlog.Field

// FieldKind — Field discriminator.
type FieldKind = ltlog.FieldKind

// Field kinds.
const (
	FieldKindString FieldKind = ltlog.FieldKindString
	FieldKindInt    FieldKind = ltlog.FieldKindInt
	FieldKindFloat  FieldKind = ltlog.FieldKindFloat
	FieldKindBool   FieldKind = ltlog.FieldKindBool
	FieldKindError  FieldKind = ltlog.FieldKindError
)

// Str creates a string Field.
func Str(key, value string) Field { return ltlog.Str(key, value) }

// Int creates an integer Field.
func Int(key string, value int64) Field { return ltlog.Int(key, value) }

// Float creates a float Field.
func Float(key string, value float64) Field { return ltlog.Float(key, value) }

// Bool creates a boolean Field.
func Bool(key string, value bool) Field { return ltlog.Bool(key, value) }

// Err creates an error Field.
func Err(err error) Field { return ltlog.Err(err) }

// NoopLogger returns the no-op logger.
func NoopLogger() Logger { return ltlog.Noop() }
