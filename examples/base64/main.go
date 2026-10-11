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
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("encoded"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `encoded:frombase64:len=4 encoded:frombase64:tobase64=encoded`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}
	for _, encoded := range []string{"AQIDBA==", "AQIDBA", "AQIDBA==\n"} {
		yes, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"encoded": hff.String(encoded)}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%q -> %t\n", encoded, yes)
	}
}
