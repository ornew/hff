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
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"), schema.Int("age"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `age>=18 name~"^A"`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}
	record := hff.Object(map[string]hff.Value{"name": hff.String("Alice"), "age": hff.Int(21)})
	match, err := filter.Match(ctx, record)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("Match:", match)
	input, err := e.Validate(ctx, record)
	if err != nil {
		log.Fatal(err)
	}
	match, err = filter.MatchInput(ctx, input)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("MatchInput:", match)
}
