package hff

import "fmt"

// Limits are per-operation ceilings. Zero selects a finite default.
// Unlimited mode is an explicit engine option, not a negative or zero number.
type Limits struct {
	MaxQueryBytes      int
	MaxSyntaxDepth     int
	MaxInputDepth      int
	MaxInputValues     int
	MaxSteps           int
	MaxGeneratedValues int
}

func DefaultLimits() Limits { return Limits{64 * 1024, 64, 64, 100000, 1000000, 100000} }
func (l Limits) Resolve() (Limits, error) {
	defaults := DefaultLimits()
	fields := []*int{&l.MaxQueryBytes, &l.MaxSyntaxDepth, &l.MaxInputDepth, &l.MaxInputValues, &l.MaxSteps, &l.MaxGeneratedValues}
	values := []int{defaults.MaxQueryBytes, defaults.MaxSyntaxDepth, defaults.MaxInputDepth, defaults.MaxInputValues, defaults.MaxSteps, defaults.MaxGeneratedValues}
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
