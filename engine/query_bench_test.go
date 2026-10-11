package engine_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// Separate byte admission from syntactic density: the padded and dense
// sources have identical length but very different numbers of predicates.
func BenchmarkDefaultQueryByteCeiling(b *testing.B) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Bool("x"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		b.Fatal(err)
	}
	limit := hff.DefaultLimits().MaxQueryBytes
	dense := strings.Repeat("x ", limit/2)
	if len(dense) < limit {
		dense += " "
	}
	for _, tc := range []struct {
		name, source string
		abort        bool
	}{
		{"padded", "x" + strings.Repeat(" ", limit-1), false},
		{"dense", dense, false},
		{"one_byte_over", dense + "x", true},
	} {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				p, err := e.Compile(ctx, tc.source)
				if !tc.abort {
					if err != nil || !p.Valid() {
						b.Fatalf("expected a complete compile: %v", err)
					}
				} else {
					var detail *hff.Error
					if p.Valid() || !errors.As(err, &detail) || detail.Code != hff.LimitError || detail.Message != "query byte limit exceeded" {
						b.Fatalf("expected byte admission abort: %v", err)
					}
				}
			}
			b.ReportMetric(float64(len(tc.source)), "query-bytes")
		})
	}
}
