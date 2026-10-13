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

func TestJSONBytesScalarUnionBuild(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.BytesType, schema.BoolType, schema.IntType, schema.FloatType, schema.NullType)),
		schema.List("items", schema.Union(schema.BytesType, schema.BoolType, schema.IntType, schema.FloatType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`value:type="bytes"`, `value:type="bool"`, `value:len=1`, `value:[bytes]:len=1`, `value:[int]>0`,
		`value="YQ==":frombase64`, `value=value`, `value!=value`,
		`value=1`, `value=true`, `value>0`, `value:in("YQ==":frombase64,1)`, `value:string="YQ=="`,
		`value:json="\"YQ==\""`, `items:contains("YQ==":frombase64)`,
		`items=items`, `items:unique`, `items:sort=null`, `items:map(x=>x:type):contains("bytes")`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatalf("%s: compile: %v", source, err)
		}
		for _, tc := range []struct {
			name   string
			format hffsql.JSONFormat
			meta   bool
		}{{"json", hffsql.JSON, false}, {"jsonb-metadata", hffsql.JSONB, true}} {
			t.Run(tc.name+"/"+source, func(t *testing.T) {
				options := []hffsql.JSONBindingOption{}
				if tc.meta {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
				}
				_, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), tc.format, options...),
					hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), tc.format, options...),
				))
				if err != nil {
					t.Fatal(err)
				}
			})
		}
	}
	program, err := e.Compile(ctx, `value:[float]:exists`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), hffsql.JSONB),
	))
	requireCode(t, err, hff.BackendError)

	for _, other := range []hff.Type{schema.StringType, schema.DurationType, schema.TimestampType} {
		ambiguous, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("value", schema.Union(schema.BytesType, other)),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := ambiguous.Compile(ctx, `value:type="bytes"`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), hffsql.JSON),
		))
		requireCode(t, err, hff.BackendError)
	}
}

func TestJSONBytesDisjointSubtypeNeedsNoMetadata(t *testing.T) {
	ctx := context.Background()
	for _, other := range []hff.Type{schema.BoolType, schema.IntType, schema.FloatType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("value", schema.Union(schema.BytesType, other, schema.NullType)),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{
			`value:type="bytes"`, `value="YQ==":frombase64`,
			`value=value`, `value:[bytes]:len=1`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format),
				))
				if err != nil {
					t.Errorf("%s %s %s: %v", other.Key(), format, source, err)
				}
			}
		}
	}
}
