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

func TestDynamicConcatJSONBRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		data, needle        hff.Type
		metadata, supported bool
	}{
		{"dynamic native types need metadata", `data:concat(needle)`, schema.AnyType, schema.AnyType, false, false},
		{"restored types", `data:concat(needle)`, schema.AnyType, schema.AnyType, true, true},
		{"known string", `data:concat(needle)`, schema.StringType, schema.AnyType, false, true},
		{"string argument", `data:concat("foo")`, schema.AnyType, schema.AnyType, false, true},
		{"known string lists", `data:concat(needle)`, schema.ListOf(schema.StringType), schema.ListOf(schema.StringType), false, true},
		{"string-list union", `data:concat(needle)`, schema.Union(schema.ListOf(schema.StringType), schema.StringType), schema.Union(schema.ListOf(schema.StringType), schema.StringType), false, true},
		{"irrelevant scalar union", `data:concat([1])`, schema.Union(schema.ListOf(schema.IntType), schema.IntType, schema.FloatType), schema.AnyType, false, true},
		{"timestamp list", `data:concat([])`, schema.Union(schema.ListOf(schema.TimestampType), schema.StringType), schema.AnyType, false, true},
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
