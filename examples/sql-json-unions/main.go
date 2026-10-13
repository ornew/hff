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
		schema.Field("observed", schema.Union(
			schema.TimestampType, schema.IntType, schema.FloatType, schema.NullType,
		)),
		schema.List("observed_items", schema.Union(
			schema.TimestampType, schema.IntType, schema.FloatType, schema.NullType,
		)),
		schema.Field("elapsed", schema.Union(
			schema.DurationType, schema.IntType, schema.FloatType, schema.NullType,
		)),
		schema.List("elapsed_items", schema.Union(
			schema.DurationType, schema.IntType, schema.FloatType, schema.NullType,
		)),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}

	program, err := e.Compile(ctx,
		`observed:in("2026-10-01T00:00:00Z":timestamp,1.5) | elapsed:in("1h":duration,1.5) | observed_items:contains(1.5) | elapsed_items:unique`,
	)
	if err != nil {
		panic(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(
			hff.Path{"observed"},
			hffsql.Column("records", "observed"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "observed_types")),
		),
		hffsql.BindJSON(
			hff.Path{"observed_items"},
			hffsql.Column("records", "observed_items"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "observed_items_types")),
		),
		hffsql.BindJSON(
			hff.Path{"elapsed"},
			hffsql.Column("records", "elapsed"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "elapsed_types")),
		),
		hffsql.BindJSON(
			hff.Path{"elapsed_items"},
			hffsql.Column("records", "elapsed_items"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "elapsed_items_types")),
		),
	))
	if err != nil {
		panic(err)
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))

	sortProgram, err := e.Compile(ctx,
		`observed_items:sort:json="[9007199254740992.0,9007199254740993]"`,
	)
	if err != nil {
		panic(err)
	}
	sortQuery, err := hffsql.Build(ctx, sortProgram,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"observed_items"},
			hffsql.Column("records", "observed_items"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "observed_items_types")),
		)),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Sort predicate built:", sortQuery.Where() != "")
	fmt.Println("Sort parameter count:", len(sortQuery.Args()))

	callbackProgram, err := e.Compile(ctx,
		`observed_items:any(item=>item:[timestamp]) | observed_items:map(item=>item:type):contains("timestamp")`,
	)
	if err != nil {
		panic(err)
	}
	callbackQuery, err := hffsql.Build(ctx, callbackProgram,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"observed_items"},
			hffsql.Column("records", "observed_items"),
			hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "observed_items_types")),
		)),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Callback predicate built:", callbackQuery.Where() != "")
	fmt.Println("Callback parameter count:", len(callbackQuery.Args()))
}
