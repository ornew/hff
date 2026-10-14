package sql_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestDirectJSONBCollectionPredicates(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.List("tags", schema.StringType)),
		schema.Nullable(schema.Map("labels", schema.StringType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSONB),
	}
	for _, tc := range []struct{ source, fragment string }{
		{`tags=[]`, `"records"."tags" = '[]'::jsonb`},
		{`tags!=[]`, `"records"."tags" <> '[]'::jsonb`},
		{`tags:size=0`, `"records"."tags" = '[]'::jsonb`},
		{`tags:size>0`, `"records"."tags" <> '[]'::jsonb`},
		{`tags:size=2`, `jsonb_array_length("records"."tags") = `},
		{`tags`, `"records"."tags" <> '[]'::jsonb`},
		{`labels={}`, `"records"."labels" = '{}'::jsonb`},
		{`labels!={}`, `"records"."labels" <> '{}'::jsonb`},
		{`labels:size=0`, `"records"."labels" = '{}'::jsonb`},
		{`labels:size>0`, `"records"."labels" <> '{}'::jsonb`},
		{`labels`, `"records"."labels" <> '{}'::jsonb`},
	} {
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if !strings.Contains(query.Where(), tc.fragment) || strings.Contains(query.Where(), "SELECT") {
			t.Fatalf("%s did not use the direct JSONB predicate: %s", tc.source, query.Where())
		}
	}
	program, err := e.Compile(ctx, `tags!:size=0`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Where(), `"records"."tags" = '[]'::jsonb`) {
		t.Fatal("guarded receiver bypassed presence-aware lowering:", query.Where())
	}
}

func TestRelationEmptyAndZeroCardinalityUseExists(t *testing.T) {
	ctx := context.Background()
	e := relationEngine(t)
	for _, source := range []string{
		`items=[]`, `items!=[]`, `items:size=0`, `items:size!=0`,
		`items:size>0`, `items:size<=0`, `items`, `!items:size=0`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(relationBinding(t, false)))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), "EXISTS (SELECT 1 FROM") || strings.Contains(query.Where(), "count(*)") || strings.Contains(query.Where(), "json_agg") {
			t.Fatalf("%s did not use correlated existence: %s", source, query.Where())
		}
	}
}

func TestRelationCardinalityDoesNotBindUnusedPredicates(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", schema.IntType)))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("related"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "parent_id"), schema.IntType),
			hffsql.Literal(hff.Int(17)))),
		hffsql.WithRelationState(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType),
			hffsql.Literal(hff.Int(19))), hffsql.Boolean(false)),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(nil, hffsql.Column("item", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source   string
		wantArgs int
	}{
		{`items:size<0`, 0},
		{`items:size>=0`, 1},
		{`items:size=0`, 2},
		{`items=[]`, 2},
	} {
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if len(query.Args()) != tc.wantArgs {
			t.Fatalf("%s: %d parameters, want %d: %s", tc.source, len(query.Args()), tc.wantArgs, query.Where())
		}
		for i := 1; i <= tc.wantArgs; i++ {
			if !strings.Contains(query.Where(), fmt.Sprintf("$%d", i)) {
				t.Fatalf("%s: argument %d is absent from SQL: %s", tc.source, i, query.Where())
			}
		}
	}
}
