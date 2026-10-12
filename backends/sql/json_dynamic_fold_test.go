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

func TestDynamicFoldJSONBRequirements(t *testing.T) {
	for _, tc := range []struct {
		name, source        string
		data, needle        hff.Type
		metadata, supported bool
	}{
		{"fold item format preserves required metadata", `data:foldl(false,(a,x)=>x:[float]:exists)`, schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), schema.AnyType, false, false},
		{"restored fold item format", `data:foldl(false,(a,x)=>x:[float]:exists)`, schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), schema.AnyType, true, true},
		{"numeric index excludes map values", `[data[0]][0]:size=1`, schema.Union(schema.ListOf(schema.ListOf(schema.IntType)), schema.MapOf(schema.AnyType)), schema.AnyType, false, true},
		{"string index excludes list elements", `[data["key"]][0]:size=1`, schema.Union(schema.ListOf(schema.AnyType), schema.MapOf(schema.ListOf(schema.IntType))), schema.AnyType, false, true},
		{"guarded accumulator preserved in callback scope", `data:foldl(needle!,(a,x)=>a!):[float]:exists`, schema.AnyType, schema.AnyType, true, true},
		{"guarded map retains values", `data:map(x=>x!):exists`, schema.AnyType, schema.AnyType, true, true},
		{"constant fold ignores element subtypes", `data:foldl("seed",(a,x)=>a:concat("!")):exists`, schema.AnyType, schema.AnyType, false, true},
		{"mixed scalar state", `data:foldl(0.0,(a,x)=>x):exists`, schema.ListOf(schema.IntType), schema.AnyType, false, true},
		{"string-number state", `data:foldl("seed",(a,x)=>x):exists`, schema.ListOf(schema.IntType), schema.AnyType, false, true},
		{"known float item", `data:foldl(0,(a,x)=>x):[float]:exists`, schema.ListOf(schema.FloatType), schema.AnyType, false, true},
		{"identity fold retains Any", `data:foldl(null,(a,x)=>x):exists`, schema.AnyType, schema.AnyType, false, false},
		{"restored identity fold", `data:foldl(null,(a,x)=>x):exists`, schema.AnyType, schema.AnyType, true, true},
		{"guarded restored identity fold", `data:foldl(null,(a,x)=>x!):exists`, schema.AnyType, schema.AnyType, true, true},
		{"captured seed retains Any", `data:foldl(needle,(a,x)=>a):exists`, schema.AnyType, schema.AnyType, false, false},
		{"nested callback value needs metadata", `data:map(x=>[x])[0][0]:[float]:exists`, schema.AnyType, schema.AnyType, false, false},
		{"restored nested callback value", `data:map(x=>[x])[0][0]:[float]:exists`, schema.AnyType, schema.AnyType, true, true},
		{"object callback value needs metadata", `data:map(x=>{"value":x})[0].value:[float]:exists`, schema.AnyType, schema.AnyType, false, false},
		{"restored object callback value", `data:map(x=>{"value":x})[0].value:[float]:exists`, schema.AnyType, schema.AnyType, true, true},
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
