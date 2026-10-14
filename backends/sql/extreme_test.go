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

func TestPostgreSQLNumericExtremesBuild(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{schema.IntType, schema.FloatType, schema.Union(schema.IntType, schema.FloatType), schema.AnyType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`items:min>0`, `items:max>0`, `items:min(x=>x)>0`, `items:max(x=>x!)>0`} {
			program, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
			if err != nil {
				t.Fatal(source, typ.Key(), err)
			}
			if !strings.Contains(query.Where(), "ORDER BY") || !strings.Contains(query.Where(), "LIMIT 1") {
				t.Fatalf("%s does not retain the first numeric extreme: %s", source, query.Where())
			}
		}
	}
}

func TestPostgreSQLMixedExtremesRequireJSONBMetadata(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Union(schema.IntType, schema.FloatType)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`items:min:exists`, `items:max(x=>x):exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		plain := hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB)
		if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(plain)); err == nil {
			t.Fatal("ambiguous numeric extreme built without metadata")
		}
		meta := hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
		if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(meta)); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPostgreSQLFloatRelationExtremesRequireOrder(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:min:json="-0.0"`)
	if err != nil {
		t.Fatal(err)
	}
	for _, ordered := range []bool{false, true} {
		opts := []hffsql.RelationOption{
			hffsql.WithRelationSource(hffsql.Column("scores"), "score"),
			hffsql.WithCorrelation(hffsql.TrustedSQL("true")),
			hffsql.WithRelationItem(schema.FloatType, hffsql.Bind(hff.Path{}, hffsql.Column("score", "value"))),
		}
		if ordered {
			opts = append(opts, hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("score", "id"), schema.IntType)))
		}
		relation, err := hffsql.NewRelation(opts...)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
		if ordered && err != nil || !ordered && err == nil {
			t.Fatalf("ordered=%v, error=%v", ordered, err)
		}
	}
}
