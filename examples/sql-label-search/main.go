// Command sql-label-search builds one query for labels stored as either a
// single string or a list of strings. The schema resolves comparison types,
// so this JSONB binding does not need numeric metadata.
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
	labelType := schema.Union(schema.StringType, schema.ListOf(schema.StringType))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Map("labels", labelType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `labels.team:contains("platform") labels.environment="production"`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("services", "labels"), hffsql.JSONB)),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
