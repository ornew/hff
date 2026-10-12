package hff

import "github.com/ornew/hff/internal/core"

// Limits contains per-stage resource ceilings; it does not itself disable
// limits or configure every stage at once. APIs apply only their relevant
// fields. Compile fields are MaxQueryBytes
// (source bytes), MaxSyntaxDepth, MaxCallbackDepth, and MaxCompileSteps. Input
// fields are MaxInputDepth and MaxInputValues. Runtime fields are MaxSteps,
// MaxGeneratedValues, and MaxJSONDepth. Zero fields resolve to finite values
// returned by DefaultLimits; negative fields are invalid. For example,
// ValidateInput reads MaxInputDepth and MaxInputValues, while local evaluation
// reads runtime fields. Use an explicit unlimited option where an API provides
// one; leaving fields at zero selects defaults rather than unlimited work.
type Limits = core.Limits

// DefaultLimits returns HFF's current finite values for all limit fields. An
// API may use only the fields belonging to its stage; consult that API's
// configuration documentation before changing them.
func DefaultLimits() Limits { return core.DefaultLimits() }
