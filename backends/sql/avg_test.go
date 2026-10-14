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

func TestPostgreSQLNumericAverageBuilds(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		typ    hff.Type
		query  string
		format hffsql.JSONFormat
	}{
		{"integer list", schema.ListOf(schema.IntType), `items:avg>0`, hffsql.JSON},
		{"float list", schema.ListOf(schema.FloatType), `items:avg>0`, hffsql.JSON},
		{"mixed list", schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), `items:avg>0`, hffsql.JSON},
		{"any list", schema.ListOf(schema.AnyType), `items:avg>0`, hffsql.JSON},
		{"integer callback", schema.ListOf(schema.Struct(schema.Int("price"))), `items:avg(x=>x.price)>0`, hffsql.JSON},
		{"dynamic callback", schema.ListOf(schema.AnyType), `items:avg(x=>x)>0`, hffsql.JSON},
		{"jsonb integer", schema.ListOf(schema.IntType), `items:avg>0`, hffsql.JSONB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("items", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			program, err := e.Compile(ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), tc.format)))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(query.Where(), "avg(") || !strings.Contains(query.Where(), "pg_input_is_valid(") {
				t.Fatalf("average did not lower to checked SQL AVG(numeric): %s", query.Where())
			}
		})
	}
}

func TestPostgreSQLMixedAverageRequiresRecoverableJSONBNumbers(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Union(schema.IntType, schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:avg>0`)
	if err != nil {
		t.Fatal(err)
	}
	plain := hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB)
	if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(plain)); err == nil {
		t.Fatal("ambiguous JSONB numeric average built without type metadata")
	}
	withMetadata := hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB,
		hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(withMetadata)); err != nil {
		t.Fatalf("recoverable JSONB numeric average failed: %v", err)
	}
}
