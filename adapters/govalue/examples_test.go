package govalue_test

import (
	"context"
	"fmt"
	"reflect"

	"github.com/ornew/hff/adapters/govalue"
	"github.com/ornew/hff/schema"
)

func ExampleNew() {
	type Person struct {
		DisplayName string
		Years       int
	}
	converter, err := govalue.New(
		govalue.WithSchema(schema.Struct(schema.String("name"), schema.Int("age"))),
		govalue.WithStructs(govalue.StructMapping{
			Type:   reflect.TypeFor[Person](),
			Fields: map[string]string{"name": "DisplayName", "age": "Years"},
		}),
	)
	if err != nil {
		panic(err)
	}
	value, err := converter.Convert(context.Background(), Person{DisplayName: "Alice", Years: 25})
	if err != nil {
		panic(err)
	}
	name, _ := value.Lookup("name")
	text, _ := name.AsString()
	fmt.Println(text)
	// Output: Alice
}

func ExampleConverter_Convert() {
	converter, err := govalue.New(govalue.WithSchema(schema.Struct(schema.Int("age"))))
	if err != nil {
		panic(err)
	}
	// A string-keyed Go map needs no StructMapping. Conversion preserves the
	// integer kind instead of promoting it to a floating value.
	value, err := converter.Convert(context.Background(), map[string]any{"age": int64(25)})
	if err != nil {
		panic(err)
	}
	age, _ := value.Lookup("age")
	integer, ok := age.AsInt()
	fmt.Println(integer, ok)
	// Output: 25 true
}
