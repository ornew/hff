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

func TestDynamicJSONScalarComparisonsBuildWithoutLosingResultState(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data="Alice"`, `data!="Alice"`, `data<"Bob"`, `data=true`, `(data=true):type="null"`, `data=9007199254740993`, `data>1.5`, `(data:[string]=null):type="bool"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		if query.Where() == "" {
			t.Fatal(source, "empty predicate")
		}
	}
	p, err := e.Compile(context.Background(), `data=1`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB)))
	if err == nil || !strings.Contains(err.Error(), "type metadata") {
		t.Fatalf("expected recoverability rejection, got %v", err)
	}
}

func TestJSONBNumericUnionUsesSchemaSubtype(t *testing.T) {
	for _, tc := range []struct {
		name    string
		typ     hff.Type
		sources []string
	}{
		{"integer", schema.Union(schema.IntType, schema.StringType), []string{`data=1.0`, `data:tojson="1"`, `data:[int]=1`, `!data:[float]:exists`}},
		{"floating", schema.Union(schema.FloatType, schema.StringType), []string{`data=1`, `data:[float]=1.0`, `!data:[int]:exists`}},
		{"list", schema.Union(schema.ListOf(schema.FloatType), schema.StringType), []string{`data:[list[float]]:exists`}},
		{"map", schema.Union(schema.MapOf(schema.FloatType), schema.StringType), []string{`data:[map[float]]:exists`}},
		{"overlapping lists", schema.Union(schema.ListOf(schema.FloatType), schema.ListOf(schema.Union(schema.FloatType, schema.NullType)), schema.StringType), []string{`data:[list[float]]:exists`}},
		{"overlapping maps", schema.Union(schema.MapOf(schema.FloatType), schema.MapOf(schema.Union(schema.FloatType, schema.NullType)), schema.StringType), []string{`data:[map[float]]:exists`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range tc.sources {
				program, err := e.Compile(context.Background(), source)
				if err != nil {
					t.Fatal(source, err)
				}
				_, err = hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB)))
				if err != nil {
					t.Fatal(source, err)
				}
			}
		})
	}
}

func TestJSONBAmbiguousCollectionUnionStillNeedsMetadata(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(context.Background(), `data:[list[float]]:exists`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB)))
	if err == nil || !strings.Contains(err.Error(), "type metadata") {
		t.Fatalf("expected ambiguous union rejection, got %v", err)
	}
}

func TestDynamicJSONComparisonWithTypeMetadataBuilds(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data=1`, `data=9007199254740993`, `data>1.5`, `(data=1):type="null"`, `(data=1):exists`, `(data:[int|string]=1):type="bool"`} {
		program, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), "WITH RECURSIVE") {
			t.Fatal(source, "missing metadata restoration")
		}
	}
}

func TestDynamicJSONStringMethodsBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"), schema.String("name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range []string{`data:len=2`, `data:startswith("A")`, `data:endswith("e")`, `data:startswith(data)`, `name:startswith(data)`, `name:endswith(data)`, `(data:len):type="null"`, `(data:startswith(data:[string])):type="null"`} {
			program, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))))
			if err != nil {
				t.Fatal(source, err)
			}
			if !strings.Contains(query.Where(), "='string'") {
				t.Fatal(source, "missing runtime string test")
			}
		}
	}
}

func TestDynamicJSONBooleanMethodsBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"), schema.Bool("flag"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range []string{`data:not`, `data:and(flag)`, `data:or(flag)`, `flag:and(data)`, `flag:or(data)`, `(data:or(data:[bool])):type="null"`, `(flag:and(data:[bool])):type="bool"`, `(flag:or(data:[bool])):type="null"`} {
			program, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			_, err = hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format), hffsql.Bind(hff.Path{"flag"}, hffsql.Column("records", "flag"))))
			if err != nil {
				t.Fatal(source, err)
			}
		}
	}
}
