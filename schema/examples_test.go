package schema_test

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func ExampleStruct() {
	typ := schema.Struct(
		schema.String("name"),
		schema.Int("age"),
		schema.List("tags", schema.StringType),
	)
	record := hff.Object(map[string]hff.Value{
		"name": hff.String("Alice"), "age": hff.Int(25),
		"tags": hff.List(hff.String("admin")),
	})
	_, err := hff.ValidateInput(context.Background(), typ, record, hff.ValidationConfig{})
	fmt.Println(err == nil)
	// Output: true
}

func ExampleOptional() {
	typ := schema.Struct(schema.Optional(schema.String("nickname")))
	_, missing := hff.ValidateInput(context.Background(), typ, hff.Object(nil), hff.ValidationConfig{})
	_, null := hff.ValidateInput(context.Background(), typ,
		hff.Object(map[string]hff.Value{"nickname": hff.Null()}), hff.ValidationConfig{})
	fmt.Println("missing allowed:", missing == nil)
	fmt.Println("null allowed:", null == nil)
	// Output:
	// missing allowed: true
	// null allowed: false
}

func ExampleNullable() {
	typ := schema.Struct(schema.Nullable(schema.String("nickname")))
	_, missing := hff.ValidateInput(context.Background(), typ, hff.Object(nil), hff.ValidationConfig{})
	_, null := hff.ValidateInput(context.Background(), typ,
		hff.Object(map[string]hff.Value{"nickname": hff.Null()}), hff.ValidationConfig{})
	fmt.Println("missing allowed:", missing == nil)
	fmt.Println("null allowed:", null == nil)
	// Output:
	// missing allowed: true
	// null allowed: true
}

func ExampleUnion() {
	typ := schema.Struct(schema.Field("identifier", schema.Union(schema.IntType, schema.StringType)))
	for _, value := range []hff.Value{hff.Int(42), hff.String("item-42"), hff.Float(42)} {
		_, err := hff.ValidateInput(context.Background(), typ,
			hff.Object(map[string]hff.Value{"identifier": value}), hff.ValidationConfig{})
		fmt.Println(err == nil)
	}
	// Output:
	// true
	// true
	// false
}

func ExampleMapOf() {
	// Arbitrary label names all carry strings; nested lists use ListOf.
	typ := schema.Struct(
		schema.Field("labels", schema.MapOf(schema.StringType)),
		schema.Field("batches", schema.ListOf(schema.ListOf(schema.IntType))),
	)
	record := hff.Object(map[string]hff.Value{
		"labels":  hff.Object(map[string]hff.Value{"app": hff.String("auth")}),
		"batches": hff.List(hff.List(hff.Int(1), hff.Int(2))),
	})
	_, err := hff.ValidateInput(context.Background(), typ, record, hff.ValidationConfig{})
	fmt.Println(err == nil)
	// Output: true
}
