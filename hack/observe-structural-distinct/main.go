// Command observe-structural-distinct prints SQL shapes for stable structural
// deduplication. It neither connects to a database nor measures execution time.
package main

import (
	"context"
	"encoding/json"
	"os"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	out := json.NewEncoder(os.Stdout)
	for _, element := range []hff.Type{
		schema.Struct(schema.Int("n")),
		schema.ListOf(schema.Union(schema.IntType, schema.FloatType)),
		schema.AnyType,
		schema.Struct(schema.Field("payload", schema.BytesType)),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", element))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			panic(err)
		}
		p, err := e.Compile(ctx, `items:distinct:size>0`)
		if err != nil {
			panic(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...)))
			if err != nil {
				panic(err)
			}
			if err := out.Encode(map[string]any{"element": element.Key(), "format": format, "source": p.Source(), "sql_bytes": len(q.Where()), "args": q.Args(), "sql": q.Where()}); err != nil {
				panic(err)
			}
		}
	}
}
