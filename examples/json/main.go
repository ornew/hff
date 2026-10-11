package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.String("raw"), schema.Any("value"))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		log.Fatal(err)
	}

	queries := []struct {
		label string
		query string
		raw   string
		value hff.Value
	}{
		{"Parsed object", `raw:fromjson:[object]:get("name")="Ada"`, `{"name":"Ada"}`, hff.Null()},
		{"Canonical JSON", `raw:fromjson:tojson="{\"a\":1,\"z\":2}"`, `{"z":2,"a":1}`, hff.Null()},
		{"Invalid JSON", `raw:fromjson:exists`, `{"name":"Ada","name":"Grace"}`, hff.Null()},
		{"Object string", `value:tostring="{\"a\":1,\"z\":2}"`, ``, hff.Object(map[string]hff.Value{"z": hff.Int(2), "a": hff.Int(1)})},
	}

	for _, item := range queries {
		program, err := e.Compile(ctx, item.query)
		if err != nil {
			log.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			log.Fatal(err)
		}
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
			"raw":   hff.String(item.raw),
			"value": item.value,
		}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: %t\n", item.label, matched)
	}
}
