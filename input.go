package hff

import (
	"context"

	"github.com/ornew/hff/internal/core"
)

// Input is an immutable record validated against one exact schema. Its zero
// value is invalid; use ValidateInput or the engine package's Validate method
// to construct it. Backends can reuse it with MatchInput when schemas match.
type Input core.Input

// ValidationConfig selects input depth/value limits from Limits. Zero fields
// use finite defaults; Unlimited disables these limits explicitly. Other
// fields in Limits do not affect ValidateInput.
type ValidationConfig = core.ValidationConfig

// ValidateInput checks the complete Value for validity and schema compatibility.
// It returns no partial Input on error. A nil context is a configuration error.
// Zero limit fields select finite defaults; set Unlimited to disable them.
func ValidateInput(ctx context.Context, schema Type, value Value, config ValidationConfig) (Input, error) {
	input, err := core.ValidateInput(ctx, core.Type(schema), core.Value(value), config)
	return Input(input), err
}

// Value returns the immutable value held by a validated input.
func (i Input) Value() Value { return Value(core.Input(i).Value()) }

// Schema returns the exact schema contract used to validate this input.
func (i Input) Schema() Type { return Type(core.Input(i).Schema()) }

// Compatible reports whether this input is valid and has exactly the supplied
// schema. Structural compatibility is not widened or inferred.
func (i Input) Compatible(t Type) bool { return core.Input(i).Compatible(core.Type(t)) }
