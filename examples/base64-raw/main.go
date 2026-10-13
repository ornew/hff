// Command base64-raw demonstrates strict unpadded standard base64 locally and
// builds the equivalent PostgreSQL predicate without opening a connection.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.Bytes("payload"),
			schema.String("encoded"),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}

	program, err := e.Compile(ctx, `
payload:len=4
payload:base64r=encoded
encoded:frombase64r=payload
payload:base64r:frombase64r=payload
`)
	if err != nil {
		log.Fatal(err)
	}

	filter, err := local.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}
	for _, encoded := range []string{"AQIDBA", "AQIDBA==", "AQIDBB"} {
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
			"payload": hff.Bytes([]byte{1, 2, 3, 4}),
			"encoded": hff.String(encoded),
		}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%q -> %t\n", encoded, matched)
	}

	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(
			hffsql.Bind(hff.Path{"payload"}, hffsql.Column("binary_assets", "payload")),
			hffsql.Bind(hff.Path{"encoded"}, hffsql.Column("binary_assets", "encoded")),
		),
	)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("PostgreSQL predicate built: %v\n", query.Where() != "")
	fmt.Printf("Parameter count: %d\n", len(query.Args()))
}
