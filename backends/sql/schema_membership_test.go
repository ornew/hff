package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func schemaMembershipEngine(t testing.TB, element hff.Type) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", element), schema.Field("needle", element),
		schema.Nullable(schema.List("items", element)), schema.Nullable(schema.List("other", element)),
		schema.List("groups", schema.ListOf(element)),
		schema.Nullable(schema.Field("guard", schema.Struct(schema.Field("value", element)))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func schemaMembershipQuery(t testing.TB, e *engine.Engine, source string, format hffsql.JSONFormat, metadata bool) *hffsql.Query {
	t.Helper()
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(source, err)
	}
	var bindings []hffsql.Binding
	for _, name := range []string{"value", "needle", "items", "other", "groups", "guard"} {
		var options []hffsql.JSONBindingOption
		if metadata {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
		}
		bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
	if err != nil {
		t.Fatal(source, err)
	}
	return q
}

func TestSchemaMembershipUsesBoundedEquality(t *testing.T) {
	for _, typ := range []hff.Type{
		schema.Union(schema.StringType, schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType), schema.NullType),
		schema.Union(schema.StringType, schema.Struct(schema.Int("i"), schema.Optional(schema.Float("f"))), schema.Struct(schema.Int("i"), schema.Optional(schema.String("label"))), schema.NullType),
		schema.Union(schema.StringType, schema.MapOf(schema.IntType), schema.Struct(schema.Float("fixed")), schema.NullType),
	} {
		e := schemaMembershipEngine(t, typ)
		for _, source := range []string{`value:in(needle)`, `items:contains(needle)`, `items:unique`, `items:in(other)`, `groups:any(xs=>xs:contains(needle))`} {
			q := schemaMembershipQuery(t, e, source, hffsql.JSON, false)
			if strings.Contains(q.Where(), "WITH RECURSIVE") || strings.Contains(q.Where(), "json_agg(") || strings.Contains(q.Where(), "json_object_agg(") {
				t.Fatalf("%s retained generic comparison normalization: %s", source, q.Where())
			}
			if !strings.Contains(q.Where(), "FULL JOIN") {
				t.Fatal(source, "lost ordered/key-set comparison")
			}
		}
	}
}

func TestSchemaMembershipKeepsAnyAndRepresentationBoundaries(t *testing.T) {
	for _, typ := range []hff.Type{schema.AnyType, schema.Union(schema.StringType, schema.Struct(schema.Any("payload"), schema.Int("id")))} {
		e := schemaMembershipEngine(t, typ)
		for _, source := range []string{`value:in(needle)`, `items:contains(needle)`, `items:unique`} {
			q := schemaMembershipQuery(t, e, source, hffsql.JSON, false)
			if !strings.Contains(q.Where(), "WITH RECURSIVE") {
				t.Fatal(source, "lost Any equality fallback")
			}
		}
	}
	// Unrelated fields do not form an ambiguous numeric subtype. An
	// overlapping numeric subtype at one position still requires metadata.
	for _, tc := range []struct {
		typ      hff.Type
		rejected bool
	}{
		{schema.Union(schema.StringType, schema.Struct(schema.Int("i"), schema.Optional(schema.Float("f"))), schema.Struct(schema.Int("i"), schema.Optional(schema.Bool("active")))), false},
		{schema.Union(schema.StringType, schema.ListOf(schema.IntType), schema.ListOf(schema.FloatType)), true},
	} {
		e := schemaMembershipEngine(t, tc.typ)
		for _, source := range []string{`value:in(needle)`, `items:contains(needle)`, `items:unique`} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), hffsql.JSONB),
				hffsql.BindJSON(hff.Path{"needle"}, hffsql.Column("records", "needle"), hffsql.JSONB),
				hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB)))
			if (err != nil) != tc.rejected {
				t.Fatalf("%s rejected=%v want=%v: %v", source, err != nil, tc.rejected, err)
			}
		}
	}
}

func TestSchemaMembershipPreservesCancellationAtEveryCheckpoint(t *testing.T) {
	for _, element := range []hff.Type{
		schema.Union(schema.StringType, schema.Struct(schema.Float("f")), schema.Struct(schema.Float("f"), schema.Optional(schema.Bool("active")))),
		schema.Union(schema.StringType, schema.Struct(schema.Bool("b")), schema.Struct(schema.Bool("b"), schema.Optional(schema.String("label")))),
	} {
		e := schemaMembershipEngine(t, element)
		p, err := e.Compile(context.Background(), `items:contains(needle) items:unique value:in(needle)`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format),
				hffsql.BindJSON(hff.Path{"needle"}, hffsql.Column("records", "needle"), format),
				hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format))}
			baseline := &buildCancellationContext{Context: context.Background()}
			if _, err := hffsql.Build(baseline, p, options...); err != nil {
				t.Fatal(err)
			}
			for after := int64(1); after <= baseline.checks.Load(); after++ {
				base, cancel := context.WithCancel(context.Background())
				ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
				q, err := hffsql.Build(ctx, p, options...)
				cancel()
				if q != nil || !errors.Is(err, context.Canceled) {
					t.Fatalf("%s checkpoint%d returned partial query %v or lost cancellation: %v", format, after, q, err)
				}
				requireCode(t, err, hff.CancelledError)
			}
		}
	}
}
