package hff

import "github.com/ornew/hff/internal/core"

// Limits contains per-stage resource ceilings. Compile fields are MaxQueryBytes
// (source bytes), MaxSyntaxDepth, MaxCallbackDepth, and MaxCompileSteps. Input
// fields are MaxInputDepth and MaxInputValues. Runtime fields are MaxSteps,
// MaxGeneratedValues, and MaxJSONDepth. Zero fields resolve to finite values
// returned by DefaultLimits; negative fields are invalid. Use an explicit
// unlimited option where an API provides one.
type Limits = core.Limits

// DefaultLimits returns HFF's current finite limit values.
func DefaultLimits() Limits { return core.DefaultLimits() }
