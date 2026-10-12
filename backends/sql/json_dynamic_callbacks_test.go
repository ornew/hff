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

func TestDynamicCallbackJSONBRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		data, needle        hff.Type
		metadata, supported bool
	}{
		{"predicate ignores element subtypes", `data:any(x=>x:exists)`, schema.AnyType, schema.AnyType, false, true},
		{"predicate typed guard needs metadata", `data:any(x=>x:[float]:exists)`, schema.AnyType, schema.AnyType, false, false},
		{"predicate restored guard", `data:any(x=>x:[float]:exists)`, schema.AnyType, schema.AnyType, true, true},
		{"constant map ignores element subtypes", `data:map(x=>"fixed"):size>0`, schema.AnyType, schema.AnyType, false, true},
		{"constant flatmap ignores element subtypes", `data:flatmap(x=>[1]):size>0`, schema.AnyType, schema.AnyType, false, true},
		{"map retains element types", `data:map(x=>x):size>0`, schema.AnyType, schema.AnyType, false, false},
		{"restored map", `data:map(x=>x):size>0`, schema.AnyType, schema.AnyType, true, true},
		{"known list union", `data:map(x=>x):size>0`, schema.Union(schema.ListOf(schema.StringType), schema.ListOf(schema.BoolType)), schema.AnyType, false, true},
		{"ambiguous known list elements", `data:map(x=>x):size>0`, schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), schema.AnyType, false, false},
		{"flatmap discards scalar alternatives", `data:flatmap(x=>x):exists`, schema.ListOf(schema.Union(schema.ListOf(schema.IntType), schema.IntType, schema.FloatType)), schema.AnyType, false, true},
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
