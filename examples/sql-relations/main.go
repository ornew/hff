package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	item := schema.Struct(schema.Int("price"), schema.Bool("active"))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", item))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
		hffsql.WithCorrelation(hffsql.TrustedSQL(`"item"."order_id" = "orders"."id"`, hffsql.Column("item"), hffsql.Column("orders"))),
		hffsql.WithRelationItem(item, hffsql.BindStruct(nil, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"),
			hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
			hffsql.Bind(hff.Path{"active"}, hffsql.Column("item", "active")),
		)),
		hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("item", "id"), schema.IntType)),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:any(item=>item.price>100 item.active)`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("WHERE", query.Where())
	fmt.Println("Args:", query.Args())
}
