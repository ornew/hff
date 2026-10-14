// Command observe-sort-key prints SQL shapes for dynamic numeric sorting.
// It does not connect to a database or measure execution time.
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
	encoder := json.NewEncoder(os.Stdout)
	for _, typ := range []hff.Type{schema.AnyType, schema.ListOf(schema.Union(schema.IntType, schema.FloatType))} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			panic(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
			}
			for _, source := range []string{`data:sort`, `data:sort[0]>0`, `data:sort:json="[1,2.0,3]"`} {
				p, err := e.Compile(ctx, source)
				if err != nil {
					panic(err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, options...)))
				if err != nil {
					panic(err)
				}
				if err := encoder.Encode(map[string]any{"type": typ.Key(), "format": format, "source": source, "sql_bytes": len(q.Where()), "args": q.Args(), "sql": q.Where()}); err != nil {
					panic(err)
				}
			}
		}
	}
}
