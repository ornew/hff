package core

import "testing"

func TestInferredIntersectionRetainsEmptyCollectionsAndFields(t *testing.T) {
	str, integer, anyType, never := PrimitiveType(StringType), PrimitiveType(IntType), PrimitiveType(AnyType), PrimitiveType(NeverType)
	for _, tc := range []struct{ actual, target, want Type }{
		{NewListType(str), NewListType(integer), NewListType(never)},
		{NewMapType(str), NewMapType(integer), NewMapType(never)},
		{NewStructType(Field{Name: "a", Type: str}), NewMapType(integer), never},
		{NewStructType(Field{Name: "a", Type: str, Optional: true}), NewMapType(integer), NewStructType(Field{Name: "a", Type: never, Optional: true})},
		{NewStructType(Field{Name: "a", Type: anyType}), NewMapType(str), NewStructType(Field{Name: "a", Type: str, Optional: true})},
		{NewUnionType(str, integer), str, str},
		{anyType, NewListType(str), NewListType(str)},
	} {
		if got := IntersectTypes(tc.actual, tc.target); !got.Equal(tc.want) {
			t.Errorf("%s intersect %s = %s; want %s", tc.actual.Key(), tc.target.Key(), got.Key(), tc.want.Key())
		}
	}
	if got := UnionTypes(never, NewUnionType(str, never)); !got.Equal(str) {
		t.Error("inferred union retained Never", got.Key())
	}
	if got := UnionTypes(str, NewUnionType(integer, anyType)); !got.Equal(anyType) {
		t.Error("inferred union did not absorb Any", got.Key())
	}
}
