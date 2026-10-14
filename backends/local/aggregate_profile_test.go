package local_test

import (
	"context"
	"testing"

	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
)

func TestLocalFloatAggregateProfileConformance(t *testing.T) {
	ctx := context.Background()
	for _, tc := range conformance.FloatAggregateCases() {
		t.Run(tc.Name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, expectation := range []struct {
				source string
				match  bool
			}{{tc.LocalSource, true}, {tc.PostgreSQLSource, tc.LocalMatch}} {
				p, err := e.Compile(ctx, expectation.source)
				if err != nil {
					t.Fatal(err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				match, err := filter.Match(ctx, tc.Record)
				if err != nil || match != expectation.match {
					t.Fatalf("%s: Local=(%v,%v), want %v", expectation.source, match, err, expectation.match)
				}
			}
		})
	}
}
