package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.List("items", schema.FloatType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:sum>0`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, p,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("items"), hffsql.JSONB)),
	)
	if err != nil {
		log.Fatal(err)
	}
	matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
		"items": hff.List(hff.Float(1e16), hff.Float(1), hff.Float(-1e16)),
	}))
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Local items:sum>0:", matched)
	printAggregation(filter.Profile())
	printAggregation(query.Profile())
	// SQL Build reports its model without connecting to a database. The
	// application owns session settings and execution of query.Where()/Args().
}

func printAggregation(profile hff.Profile) {
	for _, detail := range profile.Details() {
		if detail.Name == "float.aggregation" {
			fmt.Printf("%s: %s\n", profile.Backend(), detail.Value)
		}
	}
}
