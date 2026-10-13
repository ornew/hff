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

func TestJSONTemporalMixedNumericRequiresPreservedNumberSubtype(t *testing.T) {
	ctx := context.Background()
	for _, temporal := range []struct {
		name string
		typ  hff.Type
	}{{"timestamp", schema.TimestampType}, {"duration", schema.DurationType}} {
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("value", schema.Union(temporal.typ, schema.IntType, schema.FloatType)),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(ctx, `value=value`)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name     string
			format   hffsql.JSONFormat
			metadata bool
			allowed  bool
		}{
			{"json", hffsql.JSON, false, true},
			{"jsonb", hffsql.JSONB, false, false},
			{"jsonb-metadata", hffsql.JSONB, true, true},
		} {
			t.Run(temporal.name+"/"+tc.name, func(t *testing.T) {
				options := []hffsql.JSONBindingOption{}
				if tc.metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				_, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), tc.format, options...),
				))
				if tc.allowed && err != nil {
					t.Fatal("recoverable schema was rejected:", err)
				}
				if !tc.allowed && err == nil {
					t.Fatal("unrecoverable JSONB schema was accepted")
				}
			})
		}
	}
}
