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

func TestJSONMetadataFixedPathSQLGrowsLinearly(t *testing.T) {
	build := func(depth int) int {
		t.Helper()
		typ := schema.FloatType
		for range depth {
			typ = schema.Struct(schema.Field("child", typ))
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		program, err := e.Compile(context.Background(), "data"+strings.Repeat(".child", depth)+">1.5")
		if err != nil {
			t.Fatal(err)
		}
		query, err := hffsql.Build(context.Background(), program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("metadata")))))
		if err != nil {
			t.Fatal(err)
		}
		pointer := strings.Repeat("/child", depth)
		found := false
		for _, arg := range query.Args() {
			if arg == pointer {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing document-relative pointer %q", pointer)
		}
		return len(query.Where())
	}
	short, deep := build(10), build(20)
	if deep > 3*short {
		t.Fatalf("nonlinear SQL growth: 10 fields=%d bytes, 20 fields=%d bytes", short, deep)
	}
}
