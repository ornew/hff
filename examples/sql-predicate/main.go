// Build a parameterized PostgreSQL predicate without executing database I/O.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.Nullable(schema.String("name")),
			schema.Nullable(schema.Int("age")),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}

	program, err := e.Compile(ctx, `age>=18.5 name:contains("api")`)
	if err != nil {
		log.Fatal(err)
	}

	query, err := sql.Build(ctx, program,
		sql.WithDialect(sql.PostgreSQL),
		sql.WithBindings(
			sql.Bind(hff.Path{"name"}, sql.Column("records", "name")),
			sql.Bind(hff.Path{"age"}, sql.Column("records", "age")),
		),
	)
	if err != nil {
		log.Fatal(err)
	}

	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
