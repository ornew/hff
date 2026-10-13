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

func TestDynamicContainsJSONBRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		data, needle        hff.Type
		metadata, supported bool
	}{
		{"string needles", `data:contains("foo")`, schema.AnyType, schema.AnyType, false, true},
		{"null needle", `data:contains(null)`, schema.AnyType, schema.AnyType, false, true},
		{"unknown numeric needle", `data:contains(needle)`, schema.AnyType, schema.AnyType, false, false},
		{"restored numeric needle", `data:contains(needle)`, schema.AnyType, schema.AnyType, true, true},
		{"known string", `data:contains(needle)`, schema.StringType, schema.AnyType, false, true},
		{"known string list", `data:contains(needle)`, schema.ListOf(schema.StringType), schema.AnyType, false, true},
		{"string-list union", `data:contains(needle)`, schema.Union(schema.ListOf(schema.StringType), schema.StringType), schema.AnyType, false, true},
		{"irrelevant scalar union", `data:contains(1)`, schema.Union(schema.ListOf(schema.IntType), schema.IntType, schema.FloatType), schema.AnyType, false, true},
		{"timestamp list with null needle", `data:contains(null)`, schema.ListOf(schema.TimestampType), schema.AnyType, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", tc.data), schema.Field("needle", tc.needle))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			program, err := e.Compile(context.Background(), tc.source)
			if err != nil {
				t.Fatal(err)
			}
			var bindings []hffsql.Binding
			for _, name := range []string{"data", "needle"} {
				var options []hffsql.JSONBindingOption
				if tc.metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column(name+"_meta")))
				}
				bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column(name), hffsql.JSONB, options...))
			}
			_, err = hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if (err == nil) != tc.supported {
				t.Fatalf("supported%v, Build error%v", tc.supported, err)
			}
		})
	}
}

func TestDynamicContainsHonorsAdmittedSignatures(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard().Without(capabilities.Method("list.contains"))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = e.Compile(context.Background(), `data:contains("foo")`); err == nil {
		t.Fatal("bare Any admitted disabled list overload")
	}
	program, err := e.Compile(context.Background(), `data:[string]:contains("foo")`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB))); err != nil {
		t.Fatal(err)
	}
}
