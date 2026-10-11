package schema_test

import (
	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
	"testing"
)

func TestDescriptorsOwnTheirStorage(t *testing.T) {
	fields := []schema.FieldDef{schema.String("name"), schema.Int("age")}
	typ := schema.Struct(fields...)
	before := typ.Key()
	fields[0] = schema.Bool("changed")
	copied := typ.Fields()
	copied[0].Name = "different"
	if typ.Key() != before {
		t.Fatal("struct descriptor changed through borrowed fields")
	}
	members := []hff.Type{schema.StringType, schema.IntType}
	union := schema.Union(members...)
	before = union.Key()
	members[0] = schema.BoolType
	copiedMembers := union.Members()
	copiedMembers[0] = schema.BoolType
	if union.Key() != before {
		t.Fatal("union descriptor changed through borrowed members")
	}
	if !union.Equal(schema.Union(schema.Union(schema.IntType, schema.StringType), schema.IntType)) {
		t.Fatal("flattened duplicate union is not canonical")
	}
}
func TestInvalidNestedTypes(t *testing.T) {
	for _, typ := range []hff.Type{
		hff.Type{}, schema.ListOf(hff.Type{}), schema.MapOf(hff.Type{}), schema.Struct(schema.Field("x", hff.Type{})), schema.Union(schema.StringType, hff.Type{}),
	} {
		if typ.Check() == nil {
			t.Fatal("invalid nested descriptor accepted")
		}
	}
	deep := schema.StringType
	for range 66 {
		deep = schema.ListOf(deep)
	}
	if deep.Check() == nil {
		t.Fatal("excessive schema depth accepted")
	}
}
