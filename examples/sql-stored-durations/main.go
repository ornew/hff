// Command sql-stored-durations builds a PostgreSQL filter over a bigint column.
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
	// The caller stores exact signed int64 nanoseconds in jobs.elapsed_ns.
	// For example: CREATE TABLE jobs (id bigint PRIMARY KEY, elapsed_ns bigint);
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Nullable(schema.Duration("elapsed")))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `elapsed:seconds>=60 elapsed<"5m":duration`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.Bind(hff.Path{"elapsed"}, hffsql.Column("jobs", "elapsed_ns"))),
	)
	if err != nil {
		log.Fatal(err)
	}
	// The caller can append the WHERE fragment to SELECT id FROM jobs and pass
	// Args to its database driver. Build never connects to PostgreSQL.
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
