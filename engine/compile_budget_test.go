package engine_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// A short, shallow query can require substantial work when its schema contains
// many fields. Isolate that work from query-byte and syntax-depth ceilings.
func TestDefaultCompileBudgetForRepeatedStructuralChecks(t *testing.T) {
	source := strings.TrimSpace(strings.Repeat("x=y ", 1000))
	if len(source) >= hff.DefaultLimits().MaxQueryBytes {
		t.Fatal("test exceeds query byte ceiling")
	}
	for _, tc := range []struct {
		fields int
		abort  bool
	}{{400, false}, {600, true}} {
		t.Run(fmt.Sprintf("fields_%d", tc.fields), func(t *testing.T) {
			fields := make([]schema.FieldDef, tc.fields)
			for i := range fields {
				fields[i] = schema.Int(fmt.Sprintf("field%04d", i))
			}
			object := schema.Struct(fields...)
			e := newEngine(t, schema.Struct(schema.Field("x", object), schema.Field("y", object)),
				engine.WithCapabilities(capabilities.Standard().Without(capabilities.ListEquality)))
			program, err := e.Compile(context.Background(), source)
			if !tc.abort {
				if err != nil || program.Source() != source {
					t.Fatalf("completed compile: %v", err)
				}
				return
			}
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != hff.LimitError || detail.Message != "compilation step limit exceeded" {
				t.Fatalf("expected compile work ceiling, got %v", err)
			}
			if detail.Span == nil {
				t.Fatal("missing diagnostic span")
			}
			if program.Source() != "" {
				t.Fatal("aborted compilation returned a partial program")
			}
			// The same query and schema work when only the compile-work ceiling rises.
			higher := newEngine(t, e.Schema(), engine.WithCapabilities(e.Capabilities()),
				engine.WithLimits(hff.Limits{MaxCompileSteps: 2000000}))
			if _, err := higher.Compile(context.Background(), source); err != nil {
				t.Fatalf("independent ceiling: %v", err)
			}
		})
	}
}

// Calibration uses the public budget boundary, outside the timed benchmark.
// It avoids exposing an implementation counter solely for measurement.
func BenchmarkDefaultCompileWorkCeiling(b *testing.B) {
	ctx := context.Background()
	source := strings.TrimSpace(strings.Repeat("x=y ", 1000))
	for _, tc := range []struct {
		fields int
		abort  bool
	}{{496, false}, {497, true}} {
		fields := make([]schema.FieldDef, tc.fields)
		for i := range fields {
			fields[i] = schema.Int(fmt.Sprintf("field%04d", i))
		}
		object := schema.Struct(fields...)
		typ := schema.Struct(schema.Field("x", object), schema.Field("y", object))
		caps := capabilities.Standard().Without(capabilities.ListEquality)
		makeEngine := func(budget int) *engine.Engine {
			e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(caps), engine.WithLimits(hff.Limits{MaxCompileSteps: budget}))
			if err != nil {
				b.Fatal(err)
			}
			return e
		}
		accepts := func(budget int) bool {
			_, err := makeEngine(budget).Compile(ctx, source)
			if err == nil {
				return true
			}
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != hff.LimitError || detail.Message != "compilation step limit exceeded" {
				b.Fatal(err)
			}
			return false
		}
		low, high := 1, 2*hff.DefaultLimits().MaxCompileSteps
		if !accepts(high) {
			b.Fatal("calibration ceiling insufficient")
		}
		for low < high {
			mid := low + (high-low)/2
			if accepts(mid) {
				high = mid
			} else {
				low = mid + 1
			}
		}
		if low < 990000 || low > 1010000 {
			b.Fatalf("workload moved away from default boundary: %d", low)
		}
		e := makeEngine(hff.DefaultLimits().MaxCompileSteps)
		b.Run(fmt.Sprintf("fields_%d", tc.fields), func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(source)))
			for b.Loop() {
				p, err := e.Compile(ctx, source)
				if tc.abort {
					var detail *hff.Error
					if !errors.As(err, &detail) || detail.Code != hff.LimitError || detail.Message != "compilation step limit exceeded" || p.Valid() {
						b.Fatalf("expected explicit compile abort, got %v", err)
					}
				} else if err != nil || !p.Valid() {
					b.Fatalf("expected completed compile, got %v", err)
				}
			}
			b.ReportMetric(float64(low), "required-steps")
		})
	}
}
