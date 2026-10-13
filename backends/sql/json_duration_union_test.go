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

// A schema-known union with distinct JSON classes has enough information to
// recover its HFF member without a type-metadata column. These queries cover
// the places that must agree on that recovered type before Build accepts it.
func TestJSONDurationIntUnionBuildsConsistently(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.DurationType, schema.IntType, schema.NullType)),
		schema.List("items", schema.Union(schema.DurationType, schema.IntType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range []string{
			`value`,
			`!value`,
			`value:type="duration"`,
			`value:nanoseconds=1`,
			`value:seconds=0.000000001`,
			`value:[duration]:nanoseconds=1`,
			`value:[int]=1`,
			`value="1ns":duration`,
			`value!="1ns":duration`,
			`value=1`,
			`value>0`,
			`value=value`,
			`value!=value`,
			`value:in("1ns":duration,1)`,
			`value:in(value)`,
			`"1ns":duration:in(value)`,
			`value:string="0.000000001s"`,
			`value:json="\"0.000000001s\""`,
			`items:contains("1ns":duration)`,
			`items[0]=items[1]`,
			`items[0]!=items[1]`,
			`items[0]<items[1]`,
			`items[0]:[duration]<items[1]:[duration]`,
			`"1ns":duration:in(items[0]:[duration])`,
			`items:unique`,
			`items:sort:json="[]"`,
			`items:sort:json="[\"0s\",\"0.000000001s\"]"`,
			`items:all(x=>x)`,
			`items:map(x=>x:type):contains("duration")`,
			`items:foldl(0,(acc,x)=>acc)=0`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatalf("%s: compile: %v", source, err)
			}
			if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format),
				hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format),
			)); err != nil {
				t.Errorf("%s %s: build: %v", format, source, err)
			}
		}
	}
}

func TestJSONDurationBoolUnionBuildsConsistently(t *testing.T) {
	ctx := context.Background()
	for _, members := range [][]hff.Type{
		{schema.DurationType, schema.BoolType, schema.NullType},
		{schema.DurationType, schema.IntType, schema.BoolType, schema.NullType},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("value", schema.Union(members...)),
			schema.List("items", schema.Union(members...)),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for _, source := range []string{
				`value`, `value:type="duration"`, `value:type="bool"`,
				`value:[bool]:not`, `value="1ns":duration`, `value=true`,
				`value=value`, `value!=value`, `value:in(true,"1ns":duration)`,
				`"1ns":duration:in(value)`, `items:contains(true)`,
				`items[0]<items[1]`,
				`items[0]:[duration]<items[1]:[duration]`,
				`"1ns":duration:in(items[0]:[duration])`,
				`value:and(items[0]:[bool])`, `value:or(items[0]:[bool])`,
				`items:unique`, `items:all(x=>x)`,
				`items:map(x=>x:type):contains("bool")`,
				`items:map(x=>x):json="[\"0.000000001s\",true]"`,
			} {
				program, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatalf("%s: compile: %v", source, err)
				}
				if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format),
					hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format),
				)); err != nil {
					t.Errorf("%s %s: build: %v", format, source, err)
				}
			}
		}
	}
}

func TestJSONAmbiguousDurationUnionIsRejected(t *testing.T) {
	ctx := context.Background()
	for _, other := range []hff.Type{schema.StringType, schema.BytesType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("value", schema.Union(schema.DurationType, other)),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, `value:type="duration"`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format),
			))
			requireCode(t, err, hff.BackendError)
		}
	}
}
