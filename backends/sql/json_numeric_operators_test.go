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

func TestDynamicArithmeticRequiresNumericRepresentation(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data+1>0`, `data/2=2.5`, `data%2=1`, `-data<0`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			for _, metadata := range []bool{false, true} {
				if format == hffsql.JSON && metadata {
					continue
				}
				var options []hffsql.JSONBindingOption
				if metadata {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "types")))
				}
				_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...)))
				if format == hffsql.JSONB && !metadata {
					requireCode(t, err, hff.BackendError)
				} else if err != nil {
					t.Fatalf("%s %s metadata=%v: %v", source, format, metadata, err)
				}
			}
		}
	}
}
