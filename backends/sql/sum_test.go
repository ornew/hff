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

func TestSumChecksProjectionAndBackendSupport(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		typ    hff.Type
		query  string
		builds bool
	}{
		{"integer list", schema.ListOf(schema.IntType), `items:sum>0`, true},
		{"integer projection", schema.ListOf(schema.Struct(schema.Int("price"))), `items:sum(x=>x.price)>0`, true},
		{"float list", schema.ListOf(schema.FloatType), `items:sum>0`, true},
		{"float projection", schema.ListOf(schema.Struct(schema.Float("price"))), `items:sum(x=>x.price)>0`, true},
		{"mixed list", schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), `items:sum>0`, true},
		{"any list", schema.ListOf(schema.AnyType), `items:sum>0`, true},
		{"mixed callback", schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), `items:sum(x=>x)>0`, true},
		{"any callback", schema.ListOf(schema.AnyType), `items:sum(x=>x)>0`, true},
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
			_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
			if tc.builds && err != nil || !tc.builds && (err == nil || !strings.Contains(err.Error(), "sum currently requires")) {
				t.Fatalf("Build error = %v, want support %v", err, tc.builds)
			}
		})
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.Compile(ctx, `items:sum>0`); err == nil {
		t.Fatal("statically nonnumeric sum was accepted")
	}
}

func TestSumPreservesCallbackParametersForNullProjection(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Struct(schema.Nullable(schema.Int("price")))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:sum(v=>v.price:[null])=0`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	for i := range query.Args() {
		if !strings.Contains(query.Where(), fmt.Sprintf("$%d", i+1)) {
			t.Fatalf("generated SQL dropped parameter %d: %s", i+1, query.Where())
		}
	}
}

func TestMixedSumRequiresRecoverableJSONBNumbers(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Union(schema.IntType, schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:sum>0`)
	if err != nil {
		t.Fatal(err)
	}
	plain := hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB)
	if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(plain)); err == nil {
		t.Fatal("ambiguous JSONB numeric sum built without type metadata")
	}
	withMetadata := hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB,
		hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
	if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(withMetadata)); err != nil {
		t.Fatalf("recoverable JSONB numeric sum failed: %v", err)
	}
}
