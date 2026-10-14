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

func selectedMapEngine(t testing.TB, typ hff.Type) *engine.Engine {
	t.Helper()
	typ = schema.Union(typ, schema.StringType, schema.NullType)
	return selectedObjectEngine(t, typ)
}

func selectedObjectEngine(t testing.TB, typ hff.Type) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.Any("key"), schema.Nullable(schema.Field("guard", schema.Struct(schema.String("name")))), schema.List("groups", typ), schema.List("keys", schema.AnyType), schema.String("name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestSelectedKnownObjectAvoidsReceiverCheck(t *testing.T) {
	for _, typ := range []hff.Type{schema.MapOf(schema.FloatType), schema.Struct(schema.Float("target"))} {
		e := selectedObjectEngine(t, schema.NullableType(typ))
		p, err := e.Compile(context.Background(), `data:get("target")>0.0`)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, selectedMapOptions(hffsql.JSON, false)...)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(q.Where(), "='object'") {
			t.Fatal("known object retained dynamic receiver dispatch", q.Where())
		}
	}
}
func selectedMapOptions(format hffsql.JSONFormat, metadata bool) []hffsql.Option {
	var bindings []hffsql.Binding
	for _, name := range []string{"data", "key", "guard", "groups", "keys"} {
		var options []hffsql.JSONBindingOption
		if metadata {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
		}
		bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
	}
	bindings = append(bindings, hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")))
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)}
}
func selectedMapQuery(t testing.TB, typ hff.Type, source string, format hffsql.JSONFormat, metadata bool) *hffsql.Query {
	t.Helper()
	e := selectedMapEngine(t, typ)
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(source, err)
	}
	q, err := hffsql.Build(context.Background(), p, selectedMapOptions(format, metadata)...)
	if err != nil {
		t.Fatal(source, err)
	}
	return q
}
func TestSelectedMapValueAvoidsSiblingNormalization(t *testing.T) {
	for _, typ := range []hff.Type{schema.MapOf(schema.IntType), schema.MapOf(schema.FloatType), schema.MapOf(schema.StringType), schema.MapOf(schema.AnyType), schema.Union(schema.MapOf(schema.IntType), schema.MapOf(schema.FloatType))} {
		q := selectedMapQuery(t, typ, `data:get(key):exists`, hffsql.JSON, false)
		if strings.Contains(q.Where(), "json_each(") || strings.Contains(q.Where(), "json_object_agg(") {
			t.Fatal(typ, "retained whole-Map traversal", q.Where())
		}
	}
	q := selectedMapQuery(t, schema.MapOf(schema.MapOf(schema.StringType)), `data:get(key):size>0`, hffsql.JSON, false)
	if strings.Count(q.Where(), "json_each(") != 1 {
		t.Fatal("nested selected Map should normalize only its own entries", q.Where())
	}
}
func TestSelectedMapValueKeepsStructKeyDispatch(t *testing.T) {
	typ := schema.Struct(schema.Float("a"), schema.Int("b"))
	for _, typ := range []hff.Type{typ, schema.Union(typ, schema.MapOf(schema.IntType))} {
		q := selectedMapQuery(t, typ, `data:get(key):exists`, hffsql.JSON, false)
		if !strings.Contains(q.Where(), "json_object_agg(") {
			t.Fatal("heterogeneous Struct lost its per-key normalization", q.Where())
		}
	}
}
func TestSelectedMapValueKeepsFullRepresentationChecks(t *testing.T) {
	for _, typ := range []hff.Type{schema.MapOf(schema.AnyType), schema.MapOf(schema.Union(schema.IntType, schema.FloatType)), schema.MapOf(schema.ListOf(schema.Union(schema.IntType, schema.FloatType)))} {
		e := selectedMapEngine(t, typ)
		p, err := e.Compile(context.Background(), `data:get(key):exists`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, selectedMapOptions(hffsql.JSONB, false)...)
		requireCode(t, err, hff.BackendError)
		if _, err = hffsql.Build(context.Background(), p, selectedMapOptions(hffsql.JSONB, true)...); err != nil {
			t.Fatal(typ, err)
		}
	}
	for _, typ := range []hff.Type{schema.Union(schema.MapOf(schema.BytesType), schema.MapOf(schema.AnyType)), schema.Union(schema.MapOf(schema.BytesType), schema.MapOf(schema.StringType))} {
		e := selectedMapEngine(t, typ)
		p, err := e.Compile(context.Background(), `data:get(key):exists`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, selectedMapOptions(hffsql.JSON, false)...)
		requireCode(t, err, hff.BackendError)
	}
}
func TestSelectedMapValuePreservesCancellation(t *testing.T) {
	typ := schema.MapOf(schema.ListOf(schema.IntType))
	e := selectedMapEngine(t, typ)
	p, err := e.Compile(context.Background(), `groups:any(g=>g:get(key):contains(1))`)
	if err != nil {
		t.Fatal(err)
	}
	options := selectedMapOptions(hffsql.JSON, false)
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for after := int64(1); after <= baseline.checks.Load(); after++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
		q, err := hffsql.Build(ctx, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("checkpoint%d partialquery%v error%v", after, q, err)
		}
	}
}

func TestSelectedScalarGetAvoidsJSONRoundTrip(t *testing.T) {
	for _, valueType := range []hff.Type{schema.IntType, schema.FloatType, schema.StringType, schema.BoolType, schema.BytesType, schema.DurationType, schema.TimestampType} {
		typ := schema.Struct(schema.Nullable(schema.Field("target", valueType)), schema.Any("unused"))
		q := selectedMapQuery(t, typ, `data:get("target"):exists`, hffsql.JSON, false)
		if strings.Contains(q.Where(), "to_json(") || strings.Contains(q.Where(), "json_object_agg(") {
			t.Fatal(valueType, "retained result re-encoding or unused-field traversal", q.Where())
		}
	}
}

func TestSelectedMapValueKeepsFloatLossProvenance(t *testing.T) {
	typ := schema.MapOf(schema.FloatType)
	e := selectedMapEngine(t, typ)
	for _, source := range []string{`data:get(key):json="0.0"`, `data:get("target"):string="0.0"`, `groups:map(g=>g:get(key)):json="[]"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, selectedMapOptions(hffsql.JSONB, false)...)
		requireCode(t, err, hff.BackendError)
		if _, err = hffsql.Build(context.Background(), p, selectedMapOptions(hffsql.JSONB, true)...); err != nil {
			t.Fatal(source, err)
		}
	}
}
