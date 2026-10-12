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

func TestRoundingRequiresRecoverableNumericSubtypes(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"round", "ceil", "floor"} {
		feature, ok := profile.Feature(capabilities.Method("number." + method))
		if !ok || feature.Level != hff.Conditional {
			t.Fatalf("missing profile for %s", method)
		}
		p, err := e.Compile(context.Background(), "data:"+method+":json=\"1.0\"")
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB)))
		requireCode(t, err, hff.BackendError)
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("metadata")))
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), format, options...)))
			if err != nil {
				t.Fatalf("%s %s: %v", method, format, err)
			}
		}
	}
}
