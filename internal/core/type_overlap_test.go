package core

import "testing"

func TestTypeOverlapClosedObjectsAndEmptyCollections(t *testing.T) {
	integer := PrimitiveType(IntType)
	str := PrimitiveType(StringType)
	any := PrimitiveType(AnyType)
	nullable := NewUnionType(integer, PrimitiveType(NullType))
	cases := []struct {
		name string
		a, b Type
		want bool
	}{
		{"numeric subtypes", integer, PrimitiveType(FloatType), false},
		{"union branch", NewUnionType(integer, str), str, true},
		{"empty list", NewListType(integer), NewListType(str), true},
		{"empty map", NewMapType(integer), NewMapType(str), true},
		{"required conflicting field", NewStructType(Field{Name: "x", Type: integer}), NewStructType(Field{Name: "x", Type: str}), false},
		{"optional conflicting field", NewStructType(Field{Name: "x", Type: integer, Optional: true}), NewStructType(Field{Name: "x", Type: str, Optional: true}), true},
		{"closed disjoint fields", NewStructType(Field{Name: "x", Type: integer}), NewStructType(Field{Name: "y", Type: integer}), false},
		{"required nullable field may be absent", NewStructType(Field{Name: "x", Type: nullable}), NewStructType(), true},
		{"map struct type conflict", NewMapType(str), NewStructType(Field{Name: "x", Type: integer}), false},
		{"map struct optional conflict", NewMapType(str), NewStructType(Field{Name: "x", Type: integer, Optional: true}), true},
		{"map struct compatible", NewMapType(any), NewStructType(Field{Name: "x", Type: integer}), true},
		{"never is empty", PrimitiveType(NeverType), any, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := TypesOverlap(tc.a, tc.b); got != tc.want {
				t.Fatal(got, tc.want)
			}
			if got := TypesOverlap(tc.b, tc.a); got != tc.want {
				t.Fatal("asymmetric", got, tc.want)
			}
		})
	}
}
