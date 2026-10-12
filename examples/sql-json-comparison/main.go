// Command sql-json-comparison builds a parameterized filter comparing expected
// and actual values inside one JSONB document. Database execution stays with
// the application; the matching recipe supplies the table and sample rows.
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
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Field("event", schema.Struct(schema.Any("expected"), schema.Any("actual"))))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `event.expected=event.actual`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"event"}, hffsql.Column("checks", "event"), hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("checks", "event_types")),
		)),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
