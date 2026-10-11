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
		engine.WithSchema(schema.Struct(schema.String("name"), schema.Float("score"), schema.Any("label"))),
		engine.WithCapabilities(capabilities.Standard().Without(capabilities.Method("list.contains"))),
	)
	if err != nil {
		log.Fatal(err)
	}
	rec := hff.Object(map[string]hff.Value{"name": hff.String("ALICE"), "score": hff.Float(4.5), "label": hff.String("production")})
	match := func(query string, record hff.Value) bool {
		program, err := e.Compile(ctx, query)
		if err != nil {
			log.Fatal(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			log.Fatal(err)
		}
		yes, err := filter.Match(ctx, record)
		if err != nil {
			log.Fatal(err)
		}
		return yes
	}
	fmt.Println("Scalar predicate:", match(`name:lower="alice" score:round>=5`, rec))
	fmt.Println("Guarded string:", match(`label:[string]:contains("prod")`, rec))
	rec = hff.Object(map[string]hff.Value{"name": hff.String("ALICE"), "score": hff.Float(4.5), "label": hff.List(hff.String("production"))})
	fmt.Println("Guarded list:", match(`label:[string]:contains("prod")`, rec))
}
