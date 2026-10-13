package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestJSONDurationBuildsFromKnownSchema(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Duration("elapsed")),
		schema.List("items", schema.NullableType(schema.DurationType)),
		schema.Field("payload", schema.Struct(schema.Duration("elapsed"))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		bindings := []hffsql.Binding{
			hffsql.BindJSON(hff.Path{"elapsed"}, hffsql.Column("elapsed"), format),
			hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("items"), format),
			hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("payload"), format),
		}
		for _, source := range []string{
			`elapsed:nanoseconds>0`,
			`elapsed:string="1.000000001s"`,
			`elapsed:json="\"1.000000001s\""`,
			`elapsed:type="duration"`,
			`elapsed="1.000000001s":duration`,
			`payload.elapsed:nanoseconds=1`,
			`items:contains("1s":duration)`,
			`items:sort:json="[]"`,
			`items:map(x=>x:string):size=items:size`,
			`items:foldl("0s":duration,(acc,x)=>acc):nanoseconds=0`,
			`[elapsed]:json="[\"1.000000001s\"]"`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatalf("%s: compile: %v", source, err)
			}
			if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)); err != nil {
				t.Errorf("%s %s: build: %v", format, source, err)
			}
		}
	}
}

func TestJSONDurationRejectsAmbiguousConstructedList(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Duration("elapsed"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`[elapsed,"1s"]:json`, `[elapsed,("1s":frombase64)]:json`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatalf("%s: compile: %v", source, err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"elapsed"}, hffsql.Column("elapsed"), hffsql.JSON),
		))
		requireCode(t, err, hff.BackendError)
	}
}

func TestJSONConcatRejectsErasedTaggedElementIdentity(t *testing.T) {
	ctx := context.Background()
	for _, element := range []hff.Type{schema.DurationType, schema.BytesType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Nullable(schema.List("a", element)),
			schema.List("b", schema.AnyType),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, `a:concat(b)[0]:type="string"`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"a"}, hffsql.Column("a"), hffsql.JSON),
			hffsql.BindJSON(hff.Path{"b"}, hffsql.Column("b"), hffsql.JSON),
		))
		requireCode(t, err, hff.BackendError)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("a", schema.Union(schema.ListOf(schema.DurationType), schema.StringType)),
		schema.List("b", schema.AnyType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `a:concat(b):type="list"`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"a"}, hffsql.Column("a"), hffsql.JSON),
		hffsql.BindJSON(hff.Path{"b"}, hffsql.Column("b"), hffsql.JSON),
	))
	requireCode(t, err, hff.BackendError)
}
