package local_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// Count intermediate callback lists as well as the final flatmap output.
// Record construction, validation, compilation and Build are outside timing.
func BenchmarkDefaultGeneratedValueCeiling(b *testing.B) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	limit := hff.DefaultLimits().MaxGeneratedValues
	// Each item generates a three-value list and two output entries; the
	// output container itself counts once. Select adjacent cardinalities.
	for _, count := range []int{(limit - 1) / 5, (limit-1)/5 + 1} {
		b.Run(fmt.Sprintf("generated_%d", 1+5*count), func(b *testing.B) {
			values := make([]hff.Value, count)
			for i := range values {
				values[i] = hff.Int(int64(i))
			}
			input, err := e.Validate(ctx, hff.Object(map[string]hff.Value{"items": hff.List(values...)}))
			if err != nil {
				b.Fatal(err)
			}
			p, err := e.Compile(ctx, fmt.Sprintf(`items:flatmap(x=>[x,x]):size=%d`, 2*count))
			if err != nil {
				b.Fatal(err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			for b.Loop() {
				matched, err := filter.MatchInput(ctx, input)
				if 1+5*count <= limit {
					if !matched || err != nil {
						b.Fatalf("expected complete output: %v %v", matched, err)
					}
				} else {
					var detail *hff.Error
					if matched || !errors.As(err, &detail) || detail.Code != hff.LimitError || detail.Message != "generated value limit exceeded" {
						b.Fatalf("expected generated value abort: %v %v", matched, err)
					}
				}
			}
			b.ReportMetric(float64(1+5*count), "required-generated-values")
		})
	}
}
