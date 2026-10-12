package govalue_test

import (
	"context"
	"fmt"

	"github.com/ornew/hff/adapters/govalue"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

func ExampleNewBinding() {
	type Record struct {
		Name string `json:"name"`
		Age  int    `json:"age"`
	}
	binding, err := govalue.NewBinding[*Record]()
	if err != nil {
		panic(err)
	}
	e, err := engine.New(
		engine.WithSchema(binding.Schema()),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	program, err := e.Compile(ctx, `name age>=18`)
	if err != nil {
		panic(err)
	}
	filter, err := binding.Build(ctx, program)
	if err != nil {
		panic(err)
	}
	for _, record := range []*Record{{Name: "Alice", Age: 25}, {Name: "Bob", Age: 16}} {
		matched, err := filter.Match(ctx, record)
		if err != nil {
			panic(err)
		}
		if matched {
			fmt.Println(record.Name)
		}
	}
	// Output: Alice
}
