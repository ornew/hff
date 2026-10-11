package local_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
)

func TestSharedLocalConformance(t *testing.T) {
	ctx := context.Background()
	for _, tc := range conformance.Cases() {
		if tc.CompileError != "" {
			continue // Common compilation verifies rejected language features.
		}
		t.Run(tc.Name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities), engine.WithLimits(tc.Limits))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(ctx, tc.Source)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range tc.Records {
				t.Run(row.Name, func(t *testing.T) {
					matched, err := filter.Match(ctx, row.Value)
					if row.Error != "" {
						var detail *hff.Error
						if !errors.As(err, &detail) || detail.Code != row.Error {
							t.Fatalf("expected %s, got %v", row.Error, err)
						}
						if matched {
							t.Fatal("failed evaluation returned a partial match")
						}
						return
					}
					if err != nil || matched != row.Match {
						t.Fatalf("expected %v, got %v (%v)", row.Match, matched, err)
					}
				})
			}
		})
	}
}
