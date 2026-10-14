// Command observe-relation-average prints SQL shapes for a fixed relation AVG
// workload. It does not connect to a database or measure execution time.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	item := schema.Struct(schema.Nullable(schema.Int("price")), schema.Nullable(schema.Float("reading")))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.List("items", item), schema.Int("threshold"))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(item, hffsql.BindStruct(nil,
			hffsql.Boolean(true), hffsql.Boolean(false),
			hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
			hffsql.Bind(hff.Path{"reading"}, hffsql.Column("item", "reading")),
		)),
	)
	if err != nil {
		panic(err)
	}
	encoder := json.NewEncoder(os.Stdout)
	for _, source := range []string{
		`items:avg(x=>x.price)>10.0`,
		`items:avg(x=>x.reading)>10.0`,
		`items:avg(x=>x.price+threshold)>10.0`,
		`items:avg(x=>x.price!)>10.0`,
		`items:avg(x=>threshold)>10.0`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			panic(err)
		}
		q, err := hffsql.Build(ctx, p,
			hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(
				hffsql.BindRelation(hff.Path{"items"}, relation),
				hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("orders", "threshold")),
			),
		)
		if err != nil {
			panic(err)
		}
		if err := encoder.Encode(map[string]any{
			"source": source, "sql_bytes": len(q.Where()),
			"args": q.Args(), "sql": q.Where(),
		}); err != nil {
			panic(err)
		}
	}
}
