package core

import "fmt"

// Limits are per-operation ceilings. Zero selects a finite default.
// Unlimited mode is an explicit engine option, not a negative or zero number.
type Limits struct {
	MaxQueryBytes  int
	MaxSyntaxDepth int
	// MaxCallbackDepth counts lexically nested callbacks, starting at one.
	MaxCallbackDepth int
	// MaxCompileSteps bounds common compiler work, independently of evaluation.
	MaxCompileSteps int
	MaxInputDepth   int
	MaxInputValues  int
	// MaxJSONDepth bounds local JSON parsing and serialization; the root is depth one.
	MaxJSONDepth int
	// MaxSteps and MaxGeneratedValues are local evaluator budgets. SQL translation
	// does not promise to enforce these budgets in the database.
	MaxSteps           int
	MaxGeneratedValues int
}

func DefaultLimits() Limits {
	return Limits{MaxQueryBytes: 64 * 1024, MaxSyntaxDepth: 64, MaxCallbackDepth: 64,
		MaxCompileSteps: 1000000, MaxInputDepth: 64, MaxInputValues: 100000,
		MaxSteps: 1000000, MaxGeneratedValues: 100000, MaxJSONDepth: 64}
}
func (l Limits) Resolve() (Limits, error) {
	defaults := DefaultLimits()
	fields := []*int{&l.MaxQueryBytes, &l.MaxSyntaxDepth, &l.MaxCallbackDepth, &l.MaxCompileSteps, &l.MaxInputDepth, &l.MaxInputValues, &l.MaxJSONDepth, &l.MaxSteps, &l.MaxGeneratedValues}
	values := []int{defaults.MaxQueryBytes, defaults.MaxSyntaxDepth, defaults.MaxCallbackDepth, defaults.MaxCompileSteps, defaults.MaxInputDepth, defaults.MaxInputValues, defaults.MaxJSONDepth, defaults.MaxSteps, defaults.MaxGeneratedValues}
	for i, f := range fields {
		if *f < 0 {
			return Limits{}, fmt.Errorf("limits cannot be negative")
		}
		if *f == 0 {
			*f = values[i]
		}
	}
	return l, nil
}
