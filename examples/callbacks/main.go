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
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Struct(schema.String("name"), schema.Int("price"), schema.Bool("available"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	record := hff.Object(map[string]hff.Value{"items": hff.List(
		hff.Object(map[string]hff.Value{"name": hff.String("Book"), "price": hff.Int(120), "available": hff.Bool(true)}),
		hff.Object(map[string]hff.Value{"name": hff.String("Pen"), "price": hff.Int(5), "available": hff.Bool(false)}),
	)})
	for _, query := range []string{`items:any(item=>item.price>100)`, `items:all(item=>item.available)`, `items:map(item=>item.name)=["Book","Pen"]`, `items:foldl(0,(total,item)=>total+item.price)>100`} {
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
		fmt.Printf("%s -> %t\n", query, yes)
	}
}
