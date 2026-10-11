package main

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Map("labels", schema.StringType),
		schema.List("scores", schema.IntType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	program, err := e.Compile(ctx, `labels.app="auth" scores[-1]>=80`)
	if err != nil {
		panic(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"scores"}, hffsql.Column("records", "scores"), hffsql.JSONB),
	))
	if err != nil {
		panic(err)
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
