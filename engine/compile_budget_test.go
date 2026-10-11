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
