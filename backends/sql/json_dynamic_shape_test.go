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

func TestDynamicShapeJSONBRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		data, needle        hff.Type
		metadata, supported bool
	}{
		{"size", `data:size>0`, schema.AnyType, schema.AnyType, false, true},
		{"keys", `data:keys:contains("a")`, schema.AnyType, schema.AnyType, false, true},
		{"object has", `data:has("a")`, schema.AnyType, schema.AnyType, false, true},
		{"list has", `data:has(0)`, schema.AnyType, schema.AnyType, false, true},
		{"dynamic index requires metadata", `data:has(needle)`, schema.AnyType, schema.AnyType, false, false},
		{"dynamic restored index", `data:has(needle)`, schema.AnyType, schema.AnyType, true, true},
		{"known float index", `data:has(needle)`, schema.AnyType, schema.FloatType, false, true},
		{"known int index", `data:has(needle)`, schema.AnyType, schema.IntType, false, true},
		{"object-only dynamic argument", `data:has(needle)`, schema.MapOf(schema.AnyType), schema.AnyType, false, true},
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
