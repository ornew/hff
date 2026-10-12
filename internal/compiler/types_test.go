package compiler

import (
	"testing"

	"github.com/ornew/hff/internal/core"
)

func TestTypeUnionsAndAccess(t *testing.T) {
	integer := primitive(core.IntType)
	str := primitive(core.StringType)
	if !union(primitive(core.NeverType), integer, integer).Equal(integer) {
		t.Fatal("Never or duplicate branch preserved")
	}
	if union(integer, primitive(core.AnyType)).Kind() != core.AnyType {
		t.Fatal("Any must cover narrower types")
	}
	record := core.NewStructType(core.Field{Name: "age", Type: integer}, core.Field{Name: "name", Type: str, Optional: true})
	nullableName, ok := fieldType(record, "name")
	if !ok || nullableName.Kind() != core.UnionType {
		t.Fatal("optional field type lost null")
	}
	if _, ok := fieldType(record, "unknown"); ok {
		t.Fatal("unknown static field")
	}
	alternate := core.NewStructType(core.Field{Name: "other", Type: str})
	partial, ok := fieldType(union(record, alternate), "age")
	if !ok || !partial.Equal(nullable(integer)) {
		t.Fatal("partially applicable field access")
	}
	if _, ok := fieldType(primitive(core.AnyType), "unknown"); !ok {
		t.Fatal("Any field access should be dynamic")
	}
	if element(core.NewListType(union(str, integer))).Kind() != core.UnionType {
		t.Fatal("list element union")
	}
}
func TestBuiltinDeclarations(t *testing.T) {
	tests := map[string]int{"contains": 2, "size": 2, "concat": 2, "len": 2, "hours": 2, "frombase64": 1, "tobase64": 1, "any": 1, "type": 1, "not-a-method": 0}
	for name, want := range tests {
		if got := len(builtins(name)); got != want {
			t.Fatalf("%s: %d declarations, want %d", name, got, want)
		}
	}
}
