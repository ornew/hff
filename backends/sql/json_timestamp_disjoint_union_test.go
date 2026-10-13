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

func TestJSONTimestampDisjointScalarUnions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		member  hff.Type
		literal string
	}{
		{"bool", schema.BoolType, "true"},
		{"float", schema.FloatType, "1.5"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			union := schema.Union(schema.TimestampType, tc.member, schema.NullType)
			e, err := engine.New(engine.WithSchema(schema.Struct(
				schema.Field("value", union),
				schema.List("items", union),
			)), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				for _, source := range []string{
					`value:type="timestamp"`,
					`value:[timestamp]:year=2026`,
					`value=value`,
					`value:in("2026-01-01T00:00:00Z":timestamp,` + tc.literal + `)`,
					`items:contains("2026-01-01T00:00:00Z":timestamp)`,
					`items:unique`,
				} {
					program, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
						hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), format),
						hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("items"), format),
					))
					if err != nil {
						t.Errorf("%s %s: %v", format, source, err)
					}
				}
			}
		})
	}
}

func TestJSONTimestampAmbiguousNumericUnionIsRejected(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.TimestampType, schema.IntType, schema.FloatType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `value:type="timestamp"`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB),
	))
	requireCode(t, err, hff.BackendError)
}

func TestJSONBTimestampFloatUnionRejectsLossySerialization(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.TimestampType, schema.FloatType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`value:string="-0.0"`, `value:json="-0.0"`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB),
		))
		requireCode(t, err, hff.BackendError)
	}
}
