package engine_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestStructuralEqualityCapabilities(t *testing.T) {
	typ := schema.Struct(schema.Any("x"), schema.Any("y"), schema.List("tags", schema.StringType),
		schema.Map("labels", schema.StringType), schema.List("nested", schema.MapOf(schema.StringType)),
		schema.Field("object", schema.Struct(schema.List("values", schema.IntType))))
	base := capabilities.Standard().Without(capabilities.StructuralEquality)
	for _, tc := range []struct {
		query string
		needs []capabilities.ID
	}{
		{`x=1`, nil}, {`x="a"`, nil}, {`x=null`, nil}, {`x!=null`, nil},
		{`x:in(null)`, nil}, {`x:contains("a")`, nil}, {`tags:contains("a")`, nil}, {`tags:unique`, nil},
		{`x=[1]`, []capabilities.ID{capabilities.ListEquality}},
		{`x!=[1]`, []capabilities.ID{capabilities.ListEquality}},
		{`x:in([1])`, []capabilities.ID{capabilities.ListEquality}},
		{`x:contains([1])`, []capabilities.ID{capabilities.ListEquality}},
		{`tags=["a"]`, []capabilities.ID{capabilities.ListEquality}},
		{`x={"a":1}`, []capabilities.ID{capabilities.ObjectEquality}},
		{`labels={"a":"b"}`, []capabilities.ID{capabilities.ObjectEquality}},
		{`x:in({"a":1})`, []capabilities.ID{capabilities.ObjectEquality}},
		{`x:contains({"a":1})`, []capabilities.ID{capabilities.ObjectEquality}},
		{`nested=[{"a":"b"}]`, []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality}},
		{`object={"values":[1]}`, []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality}},
		{`x=y`, []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality}},
		{`x:unique`, []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality}},
		{`x:in(y)`, []capabilities.ID{capabilities.ListEquality, capabilities.ObjectEquality}},
		{`x:[list[string]]:unique`, nil},
		{`x:[list[string]]:contains("a")`, nil},
		{`x:[list[string]]=["a"]`, []capabilities.ID{capabilities.ListEquality}},
		{`x:[map[string]]={"a":"b"}`, []capabilities.ID{capabilities.ObjectEquality}},
		// Incompatible nested value types are unequal without recursively comparing containers.
		{`labels={"a":[1]}`, []capabilities.ID{capabilities.ObjectEquality}},
	} {
		t.Run(tc.query, func(t *testing.T) {
			for _, selected := range []capabilities.Set{base, base.Allow(capabilities.ListEquality), base.Allow(capabilities.ObjectEquality), base.Allow(capabilities.StructuralEquality)} {
				e := newEngine(t, typ, engine.WithCapabilities(selected))
				_, err := e.Compile(context.Background(), tc.query)
				allowed := true
				for _, id := range tc.needs {
					allowed = allowed && selected.Has(id)
				}
				if allowed {
					if err != nil {
						t.Fatal(err)
					}
				} else {
					detail := requireCode(t, err, hff.CapabilityError)
					if detail.Span == nil {
						t.Fatal("missing capability diagnostic span")
					}
				}
			}
		})
	}
}

func TestStructuralCapabilityAnalysisIsBounded(t *testing.T) {
	fields := make([]schema.FieldDef, 100)
	for i := range fields {
		fields[i] = schema.Int(fmt.Sprintf("field%d", i))
	}
	object := schema.Struct(fields...)
	typ := schema.Struct(schema.Field("x", object), schema.Field("y", object))
	limited := newEngine(t, typ, engine.WithCapabilities(capabilities.Standard().Without(capabilities.ListEquality)),
		engine.WithLimits(hff.Limits{MaxCompileSteps: 60}))
	_, err := limited.Compile(context.Background(), `x=y`)
	requireCode(t, err, hff.LimitError)
	// Permission checking must respect cancellation as well as its work ceiling.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = limited.Compile(ctx, `x=y`)
	requireCode(t, err, hff.CancelledError)
}
