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

func TestJSONMetadataBindingRequiresSupportedConfiguration(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Float("value"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `value:string="-0.0"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []hffsql.Binding{
		hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSON, hffsql.WithTypeMetadata(hffsql.Column("metadata"))),
		hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column())),
		hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB, hffsql.JSONBindingOption{}),
	} {
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err == nil {
			t.Fatal("invalid metadata binding accepted")
		}
	}
	if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("value"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("metadata"))))); err != nil {
		t.Fatal(err)
	}
}
