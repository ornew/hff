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

func TestDirectJSONBObjectScalarEquality(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Field("payload", schema.Struct(
			schema.Optional(schema.Nullable(schema.String("team"))), schema.Bool("active"), schema.Int("age"),
		))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	build := func(source string, format hffsql.JSONFormat, metadata bool) string {
		t.Helper()
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		var opts []hffsql.JSONBindingOption
		if metadata {
			opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "payload"), format, opts...)))
		if err != nil {
			t.Fatal(source, err)
		}
		return query.Where()
	}
	for _, source := range []string{`payload.team="auth"`, `payload.active=true`, `!payload.team="auth"`} {
		where := build(source, hffsql.JSONB, false)
		if !strings.Contains(where, `"records"."payload" @> jsonb_build_object(`) || strings.Contains(where, "SELECT") {
			t.Fatalf("%s did not use direct containment: %s", source, where)
		}
	}
	for _, tc := range []struct {
		source   string
		format   hffsql.JSONFormat
		metadata bool
	}{
		{`payload.team!="auth"`, hffsql.JSONB, false},
		{`payload.team=null`, hffsql.JSONB, false},
		{`payload.age=18`, hffsql.JSONB, false},
		{`payload.team="auth"`, hffsql.JSON, false},
		{`payload.team="auth"`, hffsql.JSONB, true},
	} {
		where := build(tc.source, tc.format, tc.metadata)
		if strings.Contains(where, `@> jsonb_build_object(`) {
			t.Fatalf("%s used containment for an ineligible representation: %s", tc.source, where)
		}
	}
	program, err := e.Compile(ctx, `payload.team="\u0000"`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"payload"}, hffsql.Column("records", "payload"), hffsql.JSONB)))
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("expected NUL parameter rejection, got %v", err)
	}
}

func TestDirectJSONBAllStringEquality(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("tags", schema.StringType)))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	build := func(source string, format hffsql.JSONFormat) string {
		t.Helper()
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), format)))
		if err != nil {
			t.Fatal(source, err)
		}
		return query.Where()
	}
	for _, source := range []string{`tags:all(tag=>tag="auth")`, `tags:all(tag=>"auth"=tag)`, `!tags:all(tag=>tag="auth")`} {
		where := build(source, hffsql.JSONB)
		if !strings.Contains(where, `"records"."tags" <@ jsonb_build_array(`) || strings.Contains(where, "SELECT") {
			t.Fatalf("%s did not use direct containment: %s", source, where)
		}
	}
	for _, source := range []string{`tags:all(tag=>tag!="auth")`, `tags:all(tag=>tag=null)`, `tags:all(tag=>tag="auth"|tag="dev")`} {
		where := build(source, hffsql.JSONB)
		if strings.Contains(where, `<@ jsonb_build_array(`) {
			t.Fatalf("%s used containment for a different callback: %s", source, where)
		}
	}
	if where := build(`tags:all(tag=>tag="auth")`, hffsql.JSON); strings.Contains(where, `<@ jsonb_build_array(`) {
		t.Fatal("JSON text used JSONB containment:", where)
	}
	program, err := e.Compile(ctx, `tags:all(tag=>tag="\u0000")`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB)))
	if err == nil || !strings.Contains(err.Error(), "NUL") {
		t.Fatalf("expected NUL parameter rejection, got %v", err)
	}
}
