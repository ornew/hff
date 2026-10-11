package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
		ID:         "service.string.hasprefix",
		Name:       "hasprefix",
		Receiver:   schema.StringType,
		Parameters: []hff.Type{schema.StringType},
		Result:     schema.BoolType,
		BaseCost:   1,
	})
	if err != nil {
		log.Fatal(err)
	}

	features := capabilities.Standard().Allow(capabilities.Method(declaration.ID()))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.String("name"))),
		engine.WithCapabilities(features),
		engine.WithMethods(declaration),
	)
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `name:hasprefix("Al")`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := local.Build(ctx, program, local.WithMethods(local.Implement(
		declaration,
		func(call *local.Call, receiver hff.Value, args []hff.Value) (hff.Value, error) {
			name, _ := receiver.AsString()
			prefix, _ := args[0].AsString()
			if err := call.ChargeSteps(len(name)); err != nil {
				return hff.Null(), err
			}
			return hff.Bool(strings.HasPrefix(name, prefix)), nil
		},
	)))
	if err != nil {
		log.Fatal(err)
	}

	for _, name := range []string{"Alice", "Bob"} {
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
			"name": hff.String(name),
		}))
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%s: %t\n", name, matched)
	}
}
