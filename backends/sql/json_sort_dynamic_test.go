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

func TestEmptySortHasNoEmptyOrderClause(t *testing.T) {
	ctx := context.Background()
	p := compile(t, `name:exists=[]:sort:exists`)
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.Where(), "ORDER BY )") {
		t.Fatal("empty inferred element type produced an empty ORDER BY:", q.Where())
	}
}

func TestDynamicSortRequiresRecoverableNumericTypes(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		typ    hff.Type
		source string
	}{
		{schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), `data:sort`},
		{schema.AnyType, `data:sort`},
		{schema.ListOf(schema.FloatType), `data:sort:json="[-0.0,1.0]"`},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB)))
		requireCode(t, err, hff.BackendError)
	}
}

func TestDynamicSortUsesCompactNumericOrderKey(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{schema.AnyType, schema.ListOf(schema.Union(schema.IntType, schema.FloatType))} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `data:sort`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...),
			))
			if err != nil {
				t.Fatal(err)
			}
			// Bit decomposition and arbitrary-precision powers were only needed
			// to construct the previous exact common key, not to sort values.
			if strings.Contains(q.Where(), "power(") || strings.Contains(q.Where(), "281474976710656") {
				t.Fatalf("%s/%s retained binary Float decomposition: %s", typ.Key(), format, q.Where())
			}
		}
	}
}
