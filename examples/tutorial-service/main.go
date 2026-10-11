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

type ticket struct {
	id     string
	team   string
	active bool
}

func main() {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.String("team"),
			schema.Bool("active"),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}

	// Compile and build when the saved filter is created or changed.
	program, err := e.Compile(ctx, `team="support" active=true`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}

	// Validate records once when they enter the service.
	for _, item := range []ticket{
		{id: "ticket-17", team: "support", active: true},
		{id: "ticket-18", team: "support", active: false},
	} {
		record := hff.Object(map[string]hff.Value{
			"team":   hff.String(item.team),
			"active": hff.Bool(item.active),
		})
		input, err := e.Validate(ctx, record)
		if err != nil {
			log.Fatal(err)
		}

		// Reuse validated input for this filter evaluation.
		matched, err := filter.MatchInput(ctx, input)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: %t\n", item.id, matched)
	}
}
