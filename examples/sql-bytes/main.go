// Command sql-bytes builds a PostgreSQL predicate over bytes and base64 text.
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
		engine.WithSchema(schema.Struct(schema.Bytes("payload"), schema.String("encoded"))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	p, err := e.Compile(ctx, `payload:len=4 payload:base64=encoded encoded:frombase64=payload`)
	if err != nil {
		log.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(
			hffsql.Bind(hff.Path{"payload"}, hffsql.Column("binary_assets", "payload")),
			hffsql.Bind(hff.Path{"encoded"}, hffsql.Column("binary_assets", "encoded")),
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	// Database execution belongs to the application. Pass q.Args() to its
	// driver when executing SELECT ... WHERE followed by q.Where().
	fmt.Printf("Predicate built: %v\n", q.Where() != "")
	fmt.Printf("Parameter count: %d\n", len(q.Args()))
}
