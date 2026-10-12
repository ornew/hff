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

func TestDynamicJSONPairRecoverability(t *testing.T) {
	for _, tc := range []struct {
		name                string
		typ                 hff.Type
		format              hffsql.JSONFormat
		metadata, supported bool
	}{
		{"native Any", schema.AnyType, hffsql.JSON, false, true},
		{"plain JSONB Any", schema.AnyType, hffsql.JSONB, false, false},
		{"restored Any", schema.AnyType, hffsql.JSONB, true, true},
		{"known integer", schema.Union(schema.IntType, schema.StringType), hffsql.JSONB, false, true},
		{"known float", schema.Union(schema.FloatType, schema.StringType), hffsql.JSONB, false, true},
		{"ambiguous number", schema.Union(schema.IntType, schema.FloatType), hffsql.JSONB, false, false},
		{"known list", schema.Union(schema.ListOf(schema.FloatType), schema.StringType), hffsql.JSONB, false, true},
		{"ambiguous list", schema.Union(schema.ListOf(schema.Union(schema.IntType, schema.FloatType)), schema.StringType), hffsql.JSONB, false, false},
		{"known object", schema.Union(schema.Struct(schema.Optional(schema.Float("n"))), schema.StringType), hffsql.JSONB, false, true},
		{"unknown object", schema.Union(schema.MapOf(schema.AnyType), schema.StringType), hffsql.JSONB, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", tc.typ), schema.Field("right", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(context.Background(), `left=right`)
			if err != nil {
				t.Fatal(err)
			}
			var bindings []hffsql.Binding
			for _, name := range []string{"left", "right"} {
				var options []hffsql.JSONBindingOption
				if tc.metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column(name+"_meta")))
				}
				bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column(name), tc.format, options...))
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			if (err == nil) != tc.supported {
				t.Fatalf("supported=%v, Build error=%v", tc.supported, err)
			}
		})
	}
}
