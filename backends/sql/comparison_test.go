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

func TestSchemaGuidedEqualityRetainsMetadataRequirements(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		typ    hff.Type
		source string
		allow  bool
	}{
		{"numeric_list", schema.ListOf(schema.AnyType), `value=[1]`, false},
		{"numeric_map", schema.MapOf(schema.AnyType), `value={"n":1}`, false},
		{"string_list", schema.ListOf(schema.AnyType), `value=["x",null]`, true},
		{"null_list", schema.ListOf(schema.AnyType), `value=[null]`, true},
		{"empty_list", schema.ListOf(schema.AnyType), `value=[]`, true},
		{"empty_object", schema.MapOf(schema.AnyType), `value={}`, true},
		{"string_object", schema.MapOf(schema.AnyType), `value={"n":"x"}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", tc.typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB)))
			if (err == nil) != tc.allow {
				t.Fatalf("Build allowed=%t, want %t: %v", err == nil, tc.allow, err)
			}
		})
	}
}
