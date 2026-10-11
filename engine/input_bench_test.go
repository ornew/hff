package engine_test

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

// BenchmarkDefaultInputValueCeiling measures complete record admission even
// when the predicate does not inspect the large collection. Record creation,
// compilation and backend construction are excluded from timing.
func BenchmarkDefaultInputValueCeiling(b *testing.B) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:exists`)
	if err != nil {
		b.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		b.Fatal(err)
	}
	ceiling := hff.DefaultLimits().MaxInputValues
	for _, extra := range []int{0, 1} {
		// The root object and its list count as two values.
		items := make([]hff.Value, ceiling-2+extra)
		for i := range items {
			items[i] = hff.Int(int64(i))
		}
		record := hff.Object(map[string]hff.Value{"items": hff.List(items...)})
		for _, mode := range []string{"Validate", "Match"} {
			b.Run(fmt.Sprintf("%s/values_%d", mode, ceiling+extra), func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					var completed bool
					var err error
					if mode == "Validate" {
						input, validateErr := e.Validate(ctx, record)
						completed, err = input.Compatible(e.Schema()), validateErr
					} else {
						completed, err = filter.Match(ctx, record)
					}
					if extra == 0 {
						if !completed || err != nil {
							b.Fatalf("expected completed admission: %v %v", completed, err)
						}
					} else {
						var detail *hff.Error
						if completed || !errors.As(err, &detail) || detail.Code != hff.LimitError {
							b.Fatalf("expected input ceiling abort: %v %v", completed, err)
						}
					}
				}
				b.ReportMetric(float64(ceiling+extra), "input-values")
			})
		}
	}
}
