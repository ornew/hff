// Command sql-durations builds a PostgreSQL filter over duration text.
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
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("elapsed"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	p, err := e.Compile(ctx, `elapsed:duration:seconds>=60`)
	if err != nil {
		log.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"elapsed"}, hffsql.Column("jobs", "elapsed_text"))))
	if err != nil {
		log.Fatal(err)
	}
	// Use q.Where() as the WHERE fragment in SELECT id FROM jobs. Pass q.Args()
	// to the database driver. HFF builds the query without opening a connection.
	fmt.Println("Predicate built:", q.Where() != "")
	fmt.Println("Parameter count:", len(q.Args()))
}
