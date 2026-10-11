package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// BenchmarkDefaultLimitWorkloads exercises selected finite defaults, including
// completed work at a boundary and explicit aborts. It does not establish a
// universal latency ceiling: operation counts are not wall-clock deadlines.
func BenchmarkDefaultLimitWorkloads(b *testing.B) {
	ctx := context.Background()
	limits := hff.DefaultLimits()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	check := func(b *testing.B, err error, abort bool) {
		b.Helper()
		if !abort {
			if err != nil {
				b.Fatal(err)
			}
			return
		}
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.LimitError {
			b.Fatalf("expected explicit limit abort, got %v", err)
		}
	}
	for _, abort := range []bool{false, true} {
		name, size := "query_bytes_boundary", limits.MaxQueryBytes
		if abort {
			name, size = "query_bytes_abort", size+1
		}
		source := "items" + strings.Repeat(" ", size-len("items"))
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := e.Compile(ctx, source)
				check(b, err, abort)
			}
		})
	}
	makeRecord := func(count int) hff.Value {
		items := make([]hff.Value, count)
		for i := range items {
			items[i] = hff.Int(int64(i))
		}
		return hff.Object(map[string]hff.Value{"items": hff.List(items...)})
	}
	for _, abort := range []bool{false, true} {
		// The root object and list container each consume one input value.
		name, count := "input_values_boundary", limits.MaxInputValues-2
		if abort {
			name, count = "input_values_abort", count+1
		}
		record := makeRecord(count)
		b.Run(name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := e.Validate(ctx, record)
				check(b, err, abort)
			}
		})
	}
	for _, tc := range []struct {
		name, source, failure string
		count                 int
	}{
		{"steps_abort", `items:all(x=>items:any(y=>y=x))`, "evaluation step limit exceeded", 1000},
		{"generated_values_abort", `items:map(x=>[x,x]):size>0`, "generated value limit exceeded", limits.MaxGeneratedValues / 2},
	} {
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			b.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			b.Fatal(err)
		}
		input, err := e.Validate(ctx, makeRecord(tc.count))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				matched, err := filter.MatchInput(ctx, input)
				check(b, err, true)
				if matched {
					b.Fatal("aborted evaluation returned a match")
				}
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Message != tc.failure {
					b.Fatalf("unexpected budget was exhausted: %v", err)
				}
			}
		})
	}
}
