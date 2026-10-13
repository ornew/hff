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

func TestJSONTimestampBuildsFromKnownSchema(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Timestamp("created")),
		schema.List("items", schema.NullableType(schema.TimestampType)),
		schema.List("sorted", schema.TimestampType),
		schema.Field("payload", schema.Struct(schema.Nullable(schema.Timestamp("created")))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		bindings := []hffsql.Binding{
			hffsql.BindJSON(hff.Path{"created"}, hffsql.Column("records", "created"), format),
			hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format),
			hffsql.BindJSON(hff.Path{"sorted"}, hffsql.Column("records", "sorted"), format),
			hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "payload"), format),
		}
		for _, source := range []string{
			`created=null`, `created:exists`, `created:type="timestamp"`,
			`created:[timestamp]:exists`, `created:[int]:exists`,
			`created="0000-01-01T00:00:00Z":timestamp`,
			`created:before("1970-01-01T00:00:00Z":timestamp)`,
			`created:after("1969-12-31T23:59:59.999999999Z":timestamp)`,
			`created:in("0000-01-01T00:00:00Z":timestamp,"2026-01-01T00:00:00Z":timestamp)`,
			`created:add("1ns":duration):year=2026`,
			`created:year=0`, `created:month=1`, `created:milliseconds=999`,
			`created:string="2026-01-01T00:00:00Z"`,
			`created:json="\"2026-01-01T00:00:00Z\""`,
			`payload.created:year=2026`,
			`items[0]:year=2026`, `items[0]:exists`, `items=items`, `items:contains(null)`,
			`sorted:sort[0]:year=0`,
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

func TestJSONTimestampRejectsAmbiguousOrUnsupportedOperations(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		source string
		typeOf hff.Type
	}{
		{`value:year=2026`, schema.Union(schema.TimestampType, schema.StringType)},
		{`value:type="timestamp"`, schema.Union(schema.TimestampType, schema.DurationType)},
		{`value:[timestamp]:exists`, schema.AnyType},
		{`value:year("Asia/Tokyo")=2026`, schema.TimestampType},
		{`value:string("Asia/Tokyo")="2026-01-01T09:00:00+09:00"`, schema.TimestampType},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", tc.typeOf))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatalf("%s: compile: %v", tc.source, err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB),
		))
		requireCode(t, err, hff.BackendError)
	}
}

func TestJSONTimestampIntUnionUsesDisjointJSONCategories(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("value", schema.Union(schema.TimestampType, schema.IntType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range []string{
			`value:type="timestamp"`, `value:[timestamp]:year=2026`,
			`value:before("2027-01-01T00:00:00Z":timestamp)`,
			`value="2026-01-01T00:00:00Z":timestamp`, `value=value`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatalf("%s: compile: %v", source, err)
			}
			if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), format),
			)); err != nil {
				t.Errorf("%s %s: build: %v", format, source, err)
			}
		}
	}
}
