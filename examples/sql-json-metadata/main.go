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
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Any("payload"))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}
	// JSONB stores {"balance":0}, while the sidecar records the original -0.0:
	// {"version":1,"types":{"/balance":{"type":"float","text":"-0.0"}}}
	// Store both columns together; the application guarantees their consistency.
	program, err := e.Compile(ctx, `payload:[map[float]]["balance"]:string="-0.0"`)
	if err != nil {
		panic(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(
			hff.Path{"payload"}, hffsql.Column("records", "payload"), hffsql.JSONB,
			hffsql.WithTypeMetadata(hffsql.Column("records", "payload_types")),
		)),
	)
	if err != nil {
		panic(err)
	}
	// Build performs no database access. Pass the predicate and arguments to
	// the database driver when assembling the application's SELECT statement.
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
