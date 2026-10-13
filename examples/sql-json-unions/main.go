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
		schema.Field("observed", schema.Union(schema.TimestampType, schema.FloatType, schema.NullType)),
		schema.Field("elapsed", schema.Union(schema.DurationType, schema.FloatType, schema.NullType)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}

	program, err := e.Compile(ctx, `observed:[timestamp]:year>=2020 | elapsed:[duration]:seconds>1`)
	if err != nil {
		panic(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"observed"}, hffsql.Column("records", "observed"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"elapsed"}, hffsql.Column("records", "elapsed"), hffsql.JSONB),
	))
	if err != nil {
		panic(err)
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
