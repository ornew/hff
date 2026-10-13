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

func TestDirectJSONBStringMembership(t *testing.T) {
	ctx := context.Background()
	for _, field := range []schema.FieldDef{
		schema.List("tags", schema.StringType),
		schema.Nullable(schema.List("tags", schema.StringType)),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(field, schema.String("name"))),
			engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		bindings := []hffsql.Binding{
			hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), hffsql.JSONB),
			hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
		}
		for _, source := range []string{
			`tags:contains("auth")`,
			`tags:contains("auth","dev","auth")`,
			`tags:contains("")`,
			`!tags:contains("auth")`,
			`tags:contains("auth") name="Alice"`,
			`tags:contains("auth")|name="Alice"`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if err != nil {
				t.Fatal(source, err)
			}
			if !strings.Contains(query.Where(), `"records"."tags" @> jsonb_build_array(`) || strings.Contains(query.Where(), "SELECT") {
				t.Fatalf("%s: membership was not lowered directly: %s", source, query.Where())
			}
		}
		for _, source := range []string{
			`tags:contains(name)`, `tags!:contains("auth")`,
			`tags:contains(null)`, `tags:contains("auth",null)`,
			`tags:contains("auth"):type="null"`,
			`tags:concat([]):contains("auth")`,
		} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if err != nil {
				t.Fatal(source, err)
			}
			if strings.Contains(query.Where(), "@> jsonb_build_array") {
				t.Fatalf("%s bypassed null/presence-aware general lowering: %s", source, query.Where())
			}
		}
		program, err := e.Compile(ctx, `tags:contains("\u0000")`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err == nil || !strings.Contains(err.Error(), "NUL") {
			t.Fatalf("expected NUL parameter rejection, got %v", err)
		}
	}
}

func TestDirectJSONBMembershipKeepsOtherRepresentationsGeneral(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		typ      hff.Type
		format   hffsql.JSONFormat
		metadata bool
	}{
		{"json text", schema.ListOf(schema.StringType), hffsql.JSON, false},
		{"nullable elements", schema.ListOf(schema.NullableType(schema.StringType)), hffsql.JSONB, false},
		{"numeric elements", schema.ListOf(schema.IntType), hffsql.JSONB, false},
		{"metadata", schema.ListOf(schema.StringType), hffsql.JSONB, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("tags", tc.typ))),
				engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			source := `tags:contains("auth")`
			if tc.name == "numeric elements" {
				source = `tags:contains(1)`
			}
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			var options []hffsql.JSONBindingOption
			if tc.metadata {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "tags_metadata")))
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"tags"}, hffsql.Column("records", "tags"), tc.format, options...)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(query.Where(), "@> jsonb_build_array") {
				t.Fatal("ineligible representation used containment:", query.Where())
			}
		})
	}
}
