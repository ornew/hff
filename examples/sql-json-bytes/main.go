// Command sql-json-bytes builds a predicate over schema-known JSON Bytes.
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
		engine.WithSchema(schema.Struct(schema.List("payloads", schema.BytesType))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	p, err := e.Compile(ctx, `payloads:any(p=>p:len>3) payloads:contains("AAECAw==":frombase64)`)
	if err != nil {
		log.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"payloads"}, hffsql.Column("binary_assets", "payloads"), hffsql.JSONB),
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	// Store payloads as a JSONB array of canonical base64 strings, for
	// example ["AAECAw==", "AA=="]. The schema gives each string its Bytes
	// identity; an Any or String|Bytes field cannot recover that identity.
	// Database execution belongs to the application. Pass q.Args() to its
	// driver when executing SELECT ... WHERE followed by q.Where().
	fmt.Printf("Predicate built: %v\n", q.Where() != "")
	fmt.Printf("Parameter count: %d\n", len(q.Args()))
}
