package main

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ornew/hff/adapters/govalue"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type Person struct {
	Name string
	Age  int64
}

func run() error {
	ctx := context.Background()
	typ := schema.Struct(schema.String("name"), schema.Int("age"))
	converter, err := govalue.New(
		govalue.WithSchema(typ),
		govalue.WithStructs(govalue.StructMapping{
			Type:   reflect.TypeFor[Person](),
			Fields: map[string]string{"name": "Name", "age": "Age"},
		}),
	)
	if err != nil {
		return err
	}
	e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		return err
	}
	program, err := e.Compile(ctx, `name="Alice" age>=18`)
	if err != nil {
		return err
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		return err
	}
	record, err := converter.Convert(ctx, Person{Name: "Alice", Age: 20})
	if err != nil {
		return err
	}
	matched, err := filter.Match(ctx, record)
	if err != nil {
		return err
	}
	fmt.Println("Match:", matched)
	return nil
}

func main() {
	if err := run(); err != nil {
		panic(err)
	}
}
