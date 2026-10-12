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

func TestDynamicObjectAccessJSONBRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		data, needle        hff.Type
		metadata, supported bool
	}{
		{"null key ignores Any values", `data:get(null):type="null"`, schema.MapOf(schema.AnyType), schema.AnyType, false, true},
		{"null key ignores dynamic receiver values", `data:get(null):type="null"`, schema.AnyType, schema.AnyType, false, true},
		{"constant string ignores Any field", `data:get("target")="ok"`, schema.Struct(schema.String("target"), schema.Any("other")), schema.AnyType, false, true},
		{"constant float ignores numeric union", `data:get("target"):[float]:exists`, schema.Struct(schema.Float("target"), schema.Field("other", schema.Union(schema.IntType, schema.FloatType))), schema.AnyType, false, true},
		{"Any needs metadata", `data:get(needle):exists`, schema.AnyType, schema.AnyType, false, false},
		{"Any restored", `data:get(needle):exists`, schema.AnyType, schema.AnyType, true, true},
		{"string map", `data:get(needle):exists`, schema.MapOf(schema.StringType), schema.AnyType, false, true},
		{"int-float struct", `data:values[0]:[float]:exists`, schema.Struct(schema.Float("a"), schema.Int("b")), schema.AnyType, false, true},
		{"irrelevant scalar alternatives", `data:items:size>0`, schema.Union(schema.Struct(schema.Float("a"), schema.Int("b")), schema.IntType, schema.FloatType), schema.AnyType, false, true},
		{"ambiguous list elements", `data:values:size>0`, schema.MapOf(schema.ListOf(schema.Union(schema.IntType, schema.FloatType))), schema.AnyType, false, false},
		{"restored list elements", `data:values:size>0`, schema.MapOf(schema.ListOf(schema.Union(schema.IntType, schema.FloatType))), schema.AnyType, true, true},
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
