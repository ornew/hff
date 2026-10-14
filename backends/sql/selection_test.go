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

func TestPostgreSQLListSelectionBuilds(t *testing.T) {
	ctx := context.Background()
	for _, item := range []hff.Type{schema.StringType, schema.IntType, schema.FloatType, schema.Union(schema.IntType, schema.FloatType), schema.AnyType, schema.Struct(schema.Int("n"))} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", item))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`items:distinct:size>0`, `items:find(x=>true):exists`} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
			if err != nil {
				t.Fatal(source, item.Key(), err)
			}
			if strings.Contains(source, ":distinct") && scalarSelectionFixture(item) && !strings.Contains(q.Where(), "DISTINCT ON") {
				t.Fatal("scalar distinct did not use grouped selection", q.Where())
			}
			if strings.Contains(source, ":find") && !strings.Contains(q.Where(), "LIMIT 1") {
				t.Fatal("find did not select one row", q.Where())
			}
		}
	}
}

func scalarSelectionFixture(item hff.Type) bool {
	return item.Kind() != hff.AnyType && item.Kind() != hff.StructType
}

func TestPostgreSQLStructuralDistinctGrouping(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name    string
		element hff.Type
		grouped bool
		walk    bool
	}{
		{"integer-object", schema.Struct(schema.Int("n")), true, false},
		{"nullable-map", schema.MapOf(schema.NullableType(schema.StringType)), true, false},
		{"nested-integers", schema.ListOf(schema.ListOf(schema.IntType)), true, false},
		{"mixed-numbers", schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), true, true},
		{"dynamic", schema.AnyType, true, true},
		{"tagged-bytes", schema.Struct(schema.Field("payload", schema.BytesType)), false, false},
		{"nested-tagged", schema.MapOf(schema.ListOf(schema.DurationType)), false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", tc.element))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(ctx, `items:distinct:size>0`)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(q.Where(), "DISTINCT ON") != tc.grouped {
				t.Fatal("unexpected grouping selection", q.Where())
			}
			if strings.Contains(q.Where(), "WITH RECURSIVE") != tc.walk {
				t.Fatal("unexpected dynamic key walk", q.Where())
			}
			if tc.grouped && strings.Contains(q.Where(), "WHERE NOT EXISTS") {
				t.Fatal("grouped selection retained pairwise prior-row comparisons", q.Where())
			}
		})
	}
}

func TestPostgreSQLSelectionRequiresJSONBMetadata(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Union(schema.IntType, schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`items:distinct:size>0`, `items:find(x=>true):exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, metadata := range []bool{false, true} {
			var opts []hffsql.JSONBindingOption
			if metadata {
				opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
			}
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB, opts...)))
			if (err == nil) != metadata {
				t.Fatalf("%s metadata=%v: %v", source, metadata, err)
			}
		}
	}
}

func TestPostgreSQLSelectionRetainsRecoveredStorageTypes(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`items:map(v=>[v!]):distinct:json="[]"`, `items:map(v=>[v!]):find(v=>true):json="[]"`, `items:map(v=>[v!]):distinct=[]`, `items:map(v=>[v!]):find(v=>true)=[]`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
	}
	e, err = engine.New(engine.WithSchema(schema.Struct(schema.Field("items", schema.Union(schema.ListOf(schema.ListOf(schema.BytesType)), schema.MapOf(schema.ListOf(schema.StringType)))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:find(v=>true):json="[]"`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
	if err != nil {
		t.Fatal("unused map branch polluted retained list element type", err)
	}
}
