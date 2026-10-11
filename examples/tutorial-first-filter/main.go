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
		engine.WithSchema(schema.Struct(
			schema.String("team"),
			schema.Bool("active"),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}

	program, err := e.Compile(ctx, `team="support" active=true`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}

	for _, ticket := range []struct {
		id     string
		team   string
		active bool
	}{
		{id: "ticket-17", team: "support", active: true},
		{id: "ticket-18", team: "support", active: false},
	} {
		record := hff.Object(map[string]hff.Value{
			"team":   hff.String(ticket.team),
			"active": hff.Bool(ticket.active),
		})
		matched, err := filter.Match(ctx, record)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: %t\n", ticket.id, matched)
	}
}
