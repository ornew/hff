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

func TestStringConversionProfilesAndFreshJSONBNumbers(t *testing.T) {
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"string.bool", "string.number"} {
		f, ok := profile.Feature(capabilities.Method(id))
		if !ok || f.Level != hff.Conditional {
			t.Fatalf("%s: %+v", id, f)
		}
		if id == "string.number" && !strings.Contains(f.Conditions, "PostgreSQL 16+") {
			t.Fatal("numeric validator requirement missing")
		}
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", schema.Union(schema.StringType, schema.FloatType, schema.NullType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:number:string="-0.0"`, `[data:number]:json="[-0.0]"`, `data:bool=null`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB)))
		if err != nil {
			t.Fatalf("%s: %v", source, err)
		}
	}
}
