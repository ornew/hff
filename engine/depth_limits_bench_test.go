package engine_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// BenchmarkDefaultDepthWorkloads measures accepted work at the default ceilings.
// Correctness and rejection just beyond each ceiling are tested separately.
func BenchmarkDefaultDepthWorkloads(b *testing.B) {
	ctx := context.Background()
	limits := hff.DefaultLimits()
	makeEngine := func(options ...engine.Option) *engine.Engine {
		options = append([]engine.Option{
			engine.WithSchema(schema.Struct(schema.Any("x"), schema.List("items", schema.IntType))),
			engine.WithCapabilities(capabilities.Standard()),
		}, options...)
		e, err := engine.New(options...)
		if err != nil {
			b.Fatal(err)
		}
		return e
	}
	e := makeEngine()
	for _, tc := range []struct {
		name, source string
		engine       *engine.Engine
	}{
		{"syntax", strings.Repeat("!", limits.MaxSyntaxDepth) + "x", e},
		{"callbacks", strings.Repeat("items:any(x=>", limits.MaxCallbackDepth) + "x>0" + strings.Repeat(")", limits.MaxCallbackDepth), makeEngine(engine.WithLimits(hff.Limits{MaxSyntaxDepth: 512}))},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := tc.engine.Compile(ctx, tc.source); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
	value := hff.Int(1)
	for range limits.MaxInputDepth - 1 {
		value = hff.List(value)
	}
	record := hff.Object(map[string]hff.Value{"x": value, "items": hff.List()})
	b.Run("input", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := e.Validate(ctx, record); err != nil {
				b.Fatal(err)
			}
		}
	})
	for _, method := range []string{"fromjson", "tojson"} {
		program, err := e.Compile(ctx, "x:"+method+":exists")
		if err != nil {
			b.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			b.Fatal(err)
		}
		jsonValue := hff.Int(1)
		for range limits.MaxJSONDepth - 1 {
			jsonValue = hff.List(jsonValue)
		}
		if method == "fromjson" {
			jsonValue = hff.String(strings.Repeat("[", limits.MaxJSONDepth-1) + "1" + strings.Repeat("]", limits.MaxJSONDepth-1))
		}
		input, err := e.Validate(ctx, hff.Object(map[string]hff.Value{"x": jsonValue, "items": hff.List()}))
		if err != nil {
			b.Fatal(err)
		}
		b.Run(fmt.Sprintf("json_%s", method), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				matched, err := filter.MatchInput(ctx, input)
				if err != nil || !matched {
					b.Fatalf("expected completed JSON operation, got %v (%v)", matched, err)
				}
			}
		})
	}
}
