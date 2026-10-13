package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestScalarPairsAvoidFloatSerialization(t *testing.T) {
	float := schema.Union(schema.StringType, schema.FloatType)
	integer := schema.Union(schema.StringType, schema.IntType)
	for _, tc := range []struct {
		left, right hff.Type
		source      string
	}{
		{float, float, `left=right`},
		{float, integer, `left<right`},
		{integer, float, `left<right`},
		{schema.Union(schema.StringType, schema.ListOf(schema.FloatType)), schema.Union(schema.StringType, schema.BoolType), `left=right`},
	} {
		q := pairDispatchQuery(t, tc.left, tc.right, tc.source, hffsql.JSONB, false)
		if strings.Contains(q.Where(), "generate_series(1,17)") || strings.Contains(q.Where(), "json_agg(") {
			t.Fatalf("%s retained numeric serialization or document normalization", tc.source)
		}
	}
}

func TestScalarPairsValidateMergedStoredPositions(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name string
		typ  hff.Type
	}{
		{"list_numeric_overlap", schema.Union(schema.StringType, schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType))},
		{"map_struct_numeric_overlap", schema.Union(schema.StringType, schema.MapOf(schema.IntType), schema.Struct(schema.Float("n")))},
		{"struct_field_numeric_overlap", schema.Union(schema.StringType, schema.Struct(schema.Int("n")), schema.Struct(schema.Float("n")))},
		{"nested_any", schema.Union(schema.StringType, schema.ListOf(schema.AnyType))},
		{"nested_tagged_ambiguity", schema.Union(schema.StringType, schema.ListOf(schema.Union(schema.StringType, schema.BytesType)))},
		{"nul_key", schema.Union(schema.StringType, schema.Struct(schema.Int("a\x00b")))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", tc.typ), schema.Field("right", schema.Union(schema.StringType, schema.BoolType)))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{`left=right`, `left<right`} {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("left"), hffsql.JSONB),
					hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("right"), hffsql.JSONB),
				))
				if err == nil {
					t.Fatalf("%s skipped an unrecoverable stored alternative", source)
				}
			}
		})
	}
	// Numeric subtypes in different fields do not overlap. Validating an
	// unused object alternative must not allocate column-key parameters.
	q := pairDispatchQuery(t, schema.Union(schema.StringType, schema.Struct(schema.Int("i"), schema.Float("f"))), schema.Union(schema.StringType, schema.BoolType), `left<right`, hffsql.JSONB, false)
	if len(q.Args()) != 0 {
		t.Fatalf("scalar comparison bound unused Struct keys: %v", q.Args())
	}
}
