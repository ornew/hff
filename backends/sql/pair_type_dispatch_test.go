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

func pairDispatchQuery(t testing.TB, left, right hff.Type, source string, format hffsql.JSONFormat, metadata bool) *hffsql.Query {
	t.Helper()
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", left), schema.Field("right", right))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, source)
	if err != nil {
		t.Fatal(source, err)
	}
	var bindings []hffsql.Binding
	for _, name := range []string{"left", "right"} {
		var options []hffsql.JSONBindingOption
		if metadata {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
		}
		bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
	if err != nil {
		t.Fatal(source, err)
	}
	return query
}

func TestPairDispatchGeneratesOnlyIntersectingRootCategories(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right hff.Type
		mixed, tree bool
	}{
		{"string_bool", schema.Union(schema.StringType, schema.BoolType), schema.Union(schema.StringType, schema.BoolType), false, false},
		{"string_int", schema.Union(schema.StringType, schema.IntType), schema.Union(schema.StringType, schema.IntType), false, false},
		{"string_float", schema.Union(schema.StringType, schema.FloatType), schema.Union(schema.StringType, schema.FloatType), false, false},
		{"asymmetric_numeric", schema.Union(schema.StringType, schema.IntType), schema.Union(schema.StringType, schema.FloatType), true, false},
		{"disjoint_containers", schema.Union(schema.StringType, schema.ListOf(schema.IntType)), schema.Union(schema.StringType, schema.MapOf(schema.IntType)), false, false},
		{"object_cross_schema", schema.Union(schema.StringType, schema.MapOf(schema.IntType)), schema.Union(schema.StringType, schema.Struct(schema.Int("n"))), true, true},
		{"array_union", schema.Union(schema.StringType, schema.ListOf(schema.IntType)), schema.Union(schema.StringType, schema.ListOf(schema.FloatType)), true, true},
		{"any", schema.AnyType, schema.AnyType, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			query := pairDispatchQuery(t, tc.left, tc.right, `left=right`, hffsql.JSON, false)
			where := query.Where()
			if got := strings.Contains(where, "floor("); got != tc.mixed {
				t.Fatalf("mixed numeric kernel=%v,want%v", got, tc.mixed)
			}
			if got := strings.Contains(where, "FULL JOIN"); got != tc.tree {
				t.Fatalf("recursive equality kernel=%v,want%v", got, tc.tree)
			}
			if tc.name == "string_bool" && strings.Contains(where, "__hff_float_") {
				t.Fatal("nonnumeric roots retained a Float parser")
			}
		})
	}
}

func TestPairDispatchPreservesRepresentationRejections(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{
		schema.Union(schema.StringType, schema.IntType, schema.FloatType),
		schema.AnyType,
		schema.Union(schema.StringType, schema.BytesType),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", typ), schema.Field("right", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `left=right`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("left"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("right"), hffsql.JSONB),
		))
		if err == nil {
			t.Fatalf("pair pruning authorized unrecoverable type %v", typ)
		}
	}
}
