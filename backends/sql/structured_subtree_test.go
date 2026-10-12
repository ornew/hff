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

func TestMetadataStructBindingRejectsDescendantOverride(t *testing.T) {
	ctx := context.Background()
	doc := schema.Struct(schema.Field("nested", schema.Struct(schema.Float("price"))))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("profile", schema.Struct(schema.Field("doc", doc))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `profile.doc.nested.price>0`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false), hffsql.BindJSON(hff.Path{"doc"}, hffsql.Column("doc"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("metadata")))),
		hffsql.Bind(hff.Path{"profile", "doc", "nested", "price"}, hffsql.Column("override_price")),
	))
	if err == nil || !strings.Contains(err.Error(), "structured binding overlaps a descendant binding") {
		t.Fatalf("overlap not rejected: %v", err)
	}
}
