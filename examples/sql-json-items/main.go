// Command sql-json-items filters named binary attachments in a JSONB object.
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
	// CREATE TABLE artifacts (id bigint PRIMARY KEY, attachments jsonb);
	// Store Bytes as canonical, padded base64 strings under the explicit
	// schema. JSON strings at an items() key position remain String keys.
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Nullable(schema.Map("attachments", schema.NullableType(schema.BytesType))))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `attachments:items:any(item=>item[0]:startswith("release-") item[1]:len>0)`)
	if err != nil {
		log.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"attachments"}, hffsql.Column("artifacts", "attachments"), hffsql.JSONB)),
	)
	if err != nil {
		log.Fatal(err)
	}
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
		{`{"release-manifest":"YQ=="}`, hff.Object(map[string]hff.Value{"release-manifest": hff.Bytes([]byte("a"))})},
		{`{"draft":"YQ=="}`, hff.Object(map[string]hff.Value{"draft": hff.Bytes([]byte("a"))})},
		{`{"release-manifest":""}`, hff.Object(map[string]hff.Value{"release-manifest": hff.Bytes(nil)})},
		{`{"release-manifest":null}`, hff.Object(map[string]hff.Value{"release-manifest": hff.Null()})},
		{`null`, hff.Null()},
	} {
		// Local receives the corresponding explicit Bytes values. Build only
		// creates SQL; the caller owns PostgreSQL storage and execution.
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"attachments": record.value}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s -> %t\n", record.stored, matched)
	}
}
