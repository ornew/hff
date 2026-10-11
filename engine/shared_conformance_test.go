package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/conformance"
)

func TestSharedCompileConformance(t *testing.T) {
	for _, tc := range conformance.Cases() {
		t.Run(tc.Name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(tc.Schema), engine.WithCapabilities(tc.Capabilities))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(context.Background(), tc.Source)
			if tc.CompileError != "" {
				var detail *hff.Error
				if !errors.As(err, &detail) || detail.Code != tc.CompileError {
					t.Fatalf("expected %s, got %v", tc.CompileError, err)
				}
				return
			}
			if err != nil || !p.Valid() {
				t.Fatalf("expected a valid program, got %v", err)
			}
		})
	}
}
