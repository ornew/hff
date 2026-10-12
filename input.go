package hff

import (
	"context"

	"github.com/ornew/hff/internal/core"
)

// Input is an immutable record validated against one exact schema. Its zero
// value is invalid; use ValidateInput or the engine package's Validate method
// to construct it. Backends can reuse it with MatchInput when schemas match.
type Input core.Input

// ValidationConfig selects host-input depth and value-count limits. Zero fields
// use finite defaults; Unlimited explicitly removes those two ceilings.
// Compile and runtime fields in Limits do not affect ValidateInput.
type ValidationConfig = core.ValidationConfig

// ValidateInput checks the complete value for valid HFF representations, input
// budgets, and compatibility with schema. It returns no partial Input on error;
// cancellation, limit failures, and configuration errors remain typed errors.
// A nil context or invalid schema is a configuration error. Zero input limits
// select finite defaults; set config.Unlimited to disable input depth/value
// ceilings explicitly. This check visits the whole record, even if a later
// filter reads only a few fields.
func ValidateInput(ctx context.Context, schema Type, value Value, config ValidationConfig) (Input, error) {
	input, err := core.ValidateInput(ctx, core.Type(schema), core.Value(value), config)
	return Input(input), err
}

// Value returns the immutable value held by a validated input. On an invalid
// zero Input it returns the invalid zero Value.
func (i Input) Value() Value { return Value(core.Input(i).Value()) }

// Schema returns the exact schema contract used to validate this input. On an
// invalid zero Input it returns the invalid zero Type.
func (i Input) Schema() Type { return Type(core.Input(i).Schema()) }

// Compatible reports whether i is valid and was validated against a schema
// structurally equal to t. It does not widen unions, infer compatibility, or
// convert values; invalid zero inputs and schemas return false.
func (i Input) Compatible(t Type) bool { return core.Input(i).Compatible(core.Type(t)) }
