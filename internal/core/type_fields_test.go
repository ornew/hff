package core_test

import (
	"testing"

	"github.com/ornew/hff/internal/core"
)

func TestIndexedFieldsPreserveImmutability(t *testing.T) {
	typ := core.NewStructType(core.Field{Name: "b", Type: core.PrimitiveType(core.StringType)}, core.Field{Name: "a", Type: core.PrimitiveType(core.IntType)})
	key := typ.Key()
	if typ.FieldCount() != 2 {
		t.Fatal("wrong field count")
	}
	field, ok := typ.FieldAt(0)
	if !ok || field.Name != "a" || field.Type.Kind() != core.IntType {
		t.Fatalf("unexpected indexed field: %+v", field)
	}
	field.Name = "changed"
	field.Type = core.PrimitiveType(core.BoolType)
	fields := typ.Fields()
	fields[0] = field
	first, _ := typ.FieldAt(0)
	if first.Name != "a" || first.Type.Kind() != core.IntType || typ.Key() != key {
		t.Fatal("field access exposed mutable schema storage")
	}
	for _, index := range []int{-1, 2, 1000} {
		if _, ok := typ.FieldAt(index); ok {
			t.Fatalf("accepted index %d", index)
		}
	}
	for _, empty := range []core.Type{{}, core.PrimitiveType(core.IntType), core.NewStructType()} {
		if empty.FieldCount() != 0 {
			t.Fatal("nonstruct or empty type has fields")
		}
		if _, ok := empty.FieldAt(0); ok {
			t.Fatal("nonstruct or empty type has a field")
		}
	}
}
