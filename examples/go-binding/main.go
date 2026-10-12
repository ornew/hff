package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff/adapters/govalue"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

type Person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

func main() {
	ctx := context.Background()
	binding, err := govalue.NewBinding[Person]()
	if err != nil {
		log.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(binding.Schema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		log.Fatal(err)
	}
	program, err := e.Compile(ctx, `age>=18 name`)
	if err != nil {
		log.Fatal(err)
	}
	filter, err := binding.Build(ctx, program)
	if err != nil {
		log.Fatal(err)
	}
	for _, person := range []Person{{"Alice", 24}, {"Bob", 15}} {
		matched, err := filter.Match(ctx, person)
		if err != nil {
			log.Fatal(err)
		}
		if matched {
			fmt.Println(person.Name)
		}
	}
}
