// Command sql-json-container-unions filters a JSONB duration-or-list column.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	// CREATE TABLE jobs (id bigint PRIMARY KEY, wait_windows jsonb);
	// Store "6m" rather than "360s": JSON Duration text must be canonical.
	// Lists may contain canonical duration strings or JSON null. A SQL NULL means no field.
	windows := schema.Union(
		schema.DurationType,
		schema.ListOf(schema.NullableType(schema.DurationType)),
		schema.NullType,
	)
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Optional(schema.Field("windows", windows)))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `windows:[duration]>"5m":duration | windows:[list[duration|null]]:any(x=>x>"5m":duration)`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"windows"}, hffsql.Column("jobs", "wait_windows"), hffsql.JSONB)),
	)
	if err != nil {
		log.Fatal(err)
	}
	// JSON's string/array categories distinguish the root alternatives. No
	// numeric type metadata is needed, and no DB connection is made by Build.
	fmt.Println("WHERE:", query.Where())
	fmt.Println("Args:", query.Args())

	filter, err := local.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}
	for _, record := range []struct {
		stored string
		value  hff.Value
	}{
		{`"1s"`, hff.Duration(time.Second)},
		{`"6m"`, hff.Duration(6 * time.Minute)},
		{`["1s",null,"6m"]`, hff.List(hff.Duration(time.Second), hff.Null(), hff.Duration(6*time.Minute))},
		{`[]`, hff.List()},
		{`null`, hff.Null()},
	} {
		// The declared schema interprets JSON strings as Duration values in
		// SQL; Local receives the corresponding explicit HFF Values.
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"windows": record.value}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s -> %t\n", record.stored, matched)
	}
}
