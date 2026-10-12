package hff_test

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func ExampleValue_Lookup() {
	record := hff.Object(map[string]hff.Value{"nickname": hff.Null(), "active": hff.Bool(false)})
	_, missing := record.Lookup("missing")
	nickname, present := record.Lookup("nickname")
	active, _ := record.Lookup("active")
	flag, typed := active.AsBool()
	fmt.Println("missing key exists:", missing)
	fmt.Println("nickname exists and is null:", present && nickname.Kind() == hff.NullKind)
	fmt.Println("active Boolean:", flag, typed)
	// Output:
	// missing key exists: false
	// nickname exists and is null: true
	// active Boolean: false true
}

func ExampleValue_AsList() {
	list := hff.List(hff.String("admin"), hff.String("viewer"))
	items, ok := list.AsList()
	if !ok {
		panic("expected a list")
	}
	// The returned slice is a copy; changing it does not mutate the HFF list.
	items[0] = hff.String("changed")
	original, _ := list.AsList()
	first, _ := original[0].AsString()
	fmt.Println(first)
	// Output: admin
}

func ExampleValidateInput() {
	typ := schema.Struct(schema.Int("age"))
	record := hff.Object(map[string]hff.Value{"age": hff.Int(25)})
	input, err := hff.ValidateInput(context.Background(), typ, record, hff.ValidationConfig{})
	if err != nil {
		panic(err)
	}
	fmt.Println("same schema:", input.Compatible(typ))
	fmt.Println("different numeric type:", input.Compatible(schema.Struct(schema.Float("age"))))
	// Output:
	// same schema: true
	// different numeric type: false
}
