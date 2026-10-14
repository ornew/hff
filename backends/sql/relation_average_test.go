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

func TestRelationAverageAvoidsJSONMaterialization(t *testing.T) {
	ctx := context.Background()
	item := schema.Struct(schema.Nullable(schema.Int("price")), schema.Nullable(schema.Float("reading")))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", item), schema.Int("threshold"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType), hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType))),
		hffsql.WithRelationItem(item, hffsql.BindStruct(nil, hffsql.Boolean(true), hffsql.Boolean(false), hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")), hffsql.Bind(hff.Path{"reading"}, hffsql.Column("item", "reading")))))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`items:avg(x=>x.price)>10.0`, `items:avg(x=>x.reading)>10.0`, `items:avg(x=>x.price!)>10.0`, `items:avg(x=>threshold)>10.0`, `items:avg(x=>x.price+threshold)>10.0`, `items:avg(x=>null)=null`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation), hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("orders", "threshold"))))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), "json_agg") || strings.Contains(q.Where(), "array_elements") {
			t.Fatalf("%s materialized the relation: %s", source, q.Where())
		}
		if !strings.Contains(q.Where(), "avg(") || !strings.Contains(q.Where(), `"item"."order_id" = "orders"."id"`) {
			t.Fatal("lost aggregate or correlation", q.Where())
		}
		if strings.Contains(source, "reading") && !strings.Contains(q.Where(), `avg("item"."reading"::text::numeric)`) {
			t.Fatal("Float did not use round-trip text before numeric AVG", q.Where())
		}
		if len(q.Args()) != 0 && len(q.Args()) != 1 {
			t.Fatalf("unused item keys bound as SQL args: %v", q.Args())
		}
	}
	p, err := e.Compile(ctx, `items!:avg(x=>x.price)>10.0`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation), hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("orders", "threshold"))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), "json_agg") {
		t.Fatal("guarded receiver did not retain general no-value lowering", q.Where())
	}
}

func TestNumericRelationItemAverageUsesNativeColumn(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{schema.IntType, schema.FloatType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.NullableType(typ)))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("values_table"), "item"), hffsql.WithCorrelation(hffsql.Boolean(true)), hffsql.WithRelationItem(schema.NullableType(typ), hffsql.Bind(nil, hffsql.Column("item", "value"))))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`items:avg>10.0`, `items:avg(x=>x)>10.0`, `items:avg(x=>x+1)>10.0`} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
			if err != nil {
				t.Fatal(err)
			}
			if strings.Contains(q.Where(), "json") || strings.Contains(q.Where(), "to_json") {
				t.Fatal("numeric native item average used JSON", q.Where())
			}
		}
	}
}
