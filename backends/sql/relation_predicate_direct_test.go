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

func TestRelationScalarCallbacksUsePhysicalColumns(t *testing.T) {
	ctx := context.Background()
	item := schema.Struct(schema.Nullable(schema.Int("price")), schema.Nullable(schema.Bool("active")))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", item))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	makeBinding := func(present hffsql.SQLPredicate) hffsql.Binding {
		relation, err := hffsql.NewRelation(
			hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
			hffsql.WithCorrelation(hffsql.Equal(
				hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
				hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
			)),
			hffsql.WithRelationItem(item, hffsql.BindStruct(nil, present, hffsql.Boolean(false),
				hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
				hffsql.Bind(hff.Path{"active"}, hffsql.Column("item", "active")),
			)),
		)
		if err != nil {
			t.Fatal(err)
		}
		return hffsql.BindRelation(hff.Path{"items"}, relation)
	}
	for _, source := range []string{
		`items:any(x=>x.price>100 x.active)`,
		`items:all(x=>x.price>100 | x.active)`,
		`items:any(x=>!x.active)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(makeBinding(hffsql.Boolean(true))))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(query.Where(), `FROM "order_items" AS "item" CROSS JOIN LATERAL`) {
			t.Errorf("%s materialized a direct callback: %s", source, query.Where())
		}
		if !strings.Contains(query.Where(), `"item"."price"`) && strings.Contains(source, "price") {
			t.Errorf("%s lost the physical price column: %s", source, query.Where())
		}
		query, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(makeBinding(hffsql.TrustedSQL(`item.item_present`, hffsql.Column("item")))))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), `FROM "order_items" AS "item" CROSS JOIN LATERAL`) {
			t.Errorf("%s lost the nullable-item fallback: %s", source, query.Where())
		}
	}
}

func TestRelationScalarItemCallbackUsesPhysicalColumn(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(nil, hffsql.Column("item", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:any(x=>x>10)`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Where(), `FROM "order_items" AS "item" CROSS JOIN LATERAL`) {
		t.Fatal("scalar callback materialized its physical item:", query.Where())
	}
	if !strings.Contains(query.Where(), `"item"."value" >`) {
		t.Fatal("scalar callback lost its physical item column:", query.Where())
	}
}
