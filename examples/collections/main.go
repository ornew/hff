package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.List("tags", schema.StringType), schema.Map("labels", schema.NullableType(schema.StringType)))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	rec := hff.Object(map[string]hff.Value{
		"tags":   hff.List(hff.String("api"), hff.String("production")),
		"labels": hff.Object(map[string]hff.Value{"app": hff.String("auth"), "team": hff.Null()}),
	})
	for _, query := range []string{
		`tags:contains("api","production") labels:has("team")`,
		`tags:count(tag=>tag:startswith("pro"))=1`,
		`tags:filter(tag=>tag:startswith("pro"))=["production"]`,
		`tags:sort=["api","production"]`,
		`labels:keys=["app","team"]`,
	} {
		program, err := e.Compile(ctx, query)
		if err != nil {
			log.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			log.Fatal(err)
		}
		match, err := filter.Match(ctx, rec)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s -> %t\n", query, match)
	}
}
