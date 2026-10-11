// Command configuration demonstrates engine construction and record validation.
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"), schema.Int("age"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	input, err := e.Validate(context.Background(), hff.Object(map[string]hff.Value{"name": hff.String("Alice"), "age": hff.Int(18)}))
	if err != nil {
		log.Fatal(err)
	}
	name, _ := input.Value().Lookup("name")
	text, _ := name.AsString()
	fmt.Println(text, input.Compatible(schema.Struct(schema.Int("age"), schema.String("name"))))
}
