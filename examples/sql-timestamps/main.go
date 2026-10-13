// Command sql-timestamps builds a PostgreSQL filter over a nanosecond-precise Timestamp.
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
	// The caller stores events.created_at as timestamptz rounded down to a
	// whole microsecond. The SMALLINT remainder stores the next 0–999 ns.
	// Both columns are null together, and non-null rows satisfy the schema.
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Nullable(schema.Timestamp("created_at")))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx,
		`created_at:after("2026-10-01T00:00:00Z":timestamp) created_at:year=2026`,
	)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindTimestamp(
			hff.Path{"created_at"}, hffsql.Column("events", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("events", "created_ns_remainder")),
		)),
	)
	if err != nil {
		log.Fatal(err)
	}
	// Append the WHERE fragment to a SELECT from events and pass Args to the
	// database driver. Build does not connect to PostgreSQL.
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
