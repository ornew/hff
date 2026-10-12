package capabilities_test

import (
	"fmt"

	"github.com/ornew/hff/capabilities"
)

func ExampleSet_Without() {
	standard := capabilities.Standard()
	policy := standard.Without(capabilities.Regex, capabilities.StructuralEquality)
	fmt.Println("regex enabled:", policy.Has(capabilities.RegexOperators))
	fmt.Println("string contains enabled:", policy.Has(capabilities.Method("string.contains")))
	fmt.Println("original unchanged:", standard.Has(capabilities.Regex))
	fmt.Println("policy valid:", policy.Check() == nil)
	// Output:
	// regex enabled: false
	// string contains enabled: true
	// original unchanged: true
	// policy valid: true
}

func ExampleSet_Allow() {
	policy := capabilities.None().Allow(
		capabilities.Logical, capabilities.Comparison, capabilities.Access,
		capabilities.Callbacks, capabilities.Method("list.any"),
	)
	fmt.Println("valid:", policy.Check() == nil)
	fmt.Println("list.any:", policy.Has(capabilities.Method("list.any")))
	fmt.Println("list.all:", policy.Has(capabilities.Method("list.all")))
	// Output:
	// valid: true
	// list.any: true
	// list.all: false
}

func ExampleSet_Check() {
	// Removing a dependency does not silently remove its dependent methods.
	policy := capabilities.Standard().Without(capabilities.Callbacks)
	fmt.Println("dependency error:", policy.Check() != nil)
	// Output: dependency error: true
}
