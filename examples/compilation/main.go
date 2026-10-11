// Compile a query into a reusable, typed plan without building an evaluator.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.String("name"), schema.Int("age"), schema.Any("labels"))),
		engine.WithCapabilities(capabilities.Standard().Without(capabilities.Method("list.contains"))),
	)
	if err != nil {
		log.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `age>=18 name~"^A" labels:[map[string]].team="platform"`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(p.Source())
	fmt.Println("Boolean predicate:", p.Root().Type().Equal(schema.BoolType))
}
