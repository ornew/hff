package sql

import (
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func TestStringFamilyMatchRetainsUnconstrainedUnionMembers(t *testing.T) {
	for _, source := range []hff.Type{
		schema.AnyType, schema.NullableType(schema.AnyType),
		schema.Union(schema.AnyType, schema.IntType, schema.StringType),
	} {
		if code, handled := jsonUnionStringMatch("value", JSON, source, schema.StringType); handled {
			t.Fatalf("%v excluded Any's possible strings: %s", source, code)
		}
	}
	for _, tc := range []struct {
		source hff.Type
		target hff.Type
		code   string
	}{
		{schema.NullableType(schema.StringType), schema.StringType, "(json_typeof(value)='string')"},
		{schema.Union(schema.BytesType, schema.IntType), schema.StringType, "FALSE"},
		{schema.Union(schema.BytesType, schema.IntType), schema.BytesType, "(json_typeof(value)='string')"},
	} {
		if code, handled := jsonUnionStringMatch("value", JSON, tc.source, tc.target); !handled || code != tc.code {
			t.Fatalf("finite union %v to %v: got %q, %v", tc.source, tc.target, code, handled)
		}
	}
}
