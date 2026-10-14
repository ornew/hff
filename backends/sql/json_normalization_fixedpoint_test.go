package sql

import (
	"context"
	"testing"

	"github.com/ornew/hff"
)

func normalizationFixtureTypes() []hff.Type {
	i, f, s := hff.PrimitiveType(hff.IntType), hff.PrimitiveType(hff.FloatType), hff.PrimitiveType(hff.StringType)
	numeric := hff.NewUnionType(i, f)
	structure := hff.NewStructType(hff.Field{Name: "f", Type: f}, hff.Field{Name: "i", Type: i})
	return []hff.Type{
		hff.PrimitiveType(hff.NullType), hff.PrimitiveType(hff.NeverType), i, f, numeric, s,
		hff.PrimitiveType(hff.BoolType), hff.PrimitiveType(hff.BytesType), hff.PrimitiveType(hff.DurationType), hff.PrimitiveType(hff.TimestampType), hff.PrimitiveType(hff.AnyType),
		hff.NewListType(hff.PrimitiveType(hff.NeverType)), hff.NewListType(f), hff.NewListType(numeric),
		hff.NewMapType(i), structure, hff.NewUnionType(hff.NewMapType(i), structure),
		hff.NewStructType(), hff.NewStructType(hff.Field{Name: "f", Type: f}),
	}
}

func TestJSONNormalizationFixedPoint(t *testing.T) {
	i, f, s := hff.PrimitiveType(hff.IntType), hff.PrimitiveType(hff.FloatType), hff.PrimitiveType(hff.StringType)
	null, never, any := hff.PrimitiveType(hff.NullType), hff.PrimitiveType(hff.NeverType), hff.PrimitiveType(hff.AnyType)
	numeric := hff.NewUnionType(i, f)
	bytes := hff.PrimitiveType(hff.BytesType)
	fieldInt := hff.NewStructType(hff.Field{Name: "f", Type: i})
	fieldFloat := hff.NewStructType(hff.Field{Name: "f", Type: f})
	overlap := hff.NewUnionType(hff.NewMapType(i), fieldFloat)
	for _, tc := range []struct {
		name           string
		source, target hff.Type
		want           bool
	}{
		{"int widening", i, numeric, true}, {"float widening", f, numeric, true},
		{"float narrowing", f, i, false}, {"int retyping", i, f, false}, {"union narrowing", numeric, f, false},
		{"nullable widening", hff.NewUnionType(null, i), numeric, true},
		{"null source", null, f, true}, {"Never source", never, f, true},
		{"empty list", hff.NewListType(never), hff.NewListType(f), true},
		{"list widening", hff.NewListType(i), hff.NewListType(numeric), true},
		{"list narrowing", hff.NewListType(numeric), hff.NewListType(f), false},
		{"nested widening", hff.NewListType(hff.NewListType(f)), hff.NewListType(hff.NewListType(numeric)), true},
		{"Any target", fieldFloat, any, true}, {"Any source", any, fieldFloat, false},
		{"bytes to string", bytes, s, false}, {"string to bytes", s, bytes, false},
		{"struct to map", fieldInt, hff.NewMapType(i), true},
		{"map loses unknown keys", hff.NewMapType(i), fieldInt, false},
		{"struct field retyping", fieldFloat, hff.NewMapType(i), false},
		{"map overlap", hff.NewMapType(i), overlap, true},
		{"struct overlap", fieldFloat, overlap, true},
		{"empty struct", hff.NewStructType(), hff.NewMapType(i), true},
		{"target-only field uses source fallback", hff.NewMapType(f), hff.NewUnionType(hff.NewMapType(f), fieldInt), true},
		{"source-only field uses target fallback", fieldFloat, hff.NewMapType(numeric), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &builder{ctx: context.Background()}
			got, err := b.normalizationPreservesJSON(tc.source, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("preserves=%t, want %t", got, tc.want)
			}
		})
	}
	for _, typ := range normalizationFixtureTypes() {
		b := &builder{ctx: context.Background()}
		if got, err := b.normalizationPreservesJSON(typ, typ); err != nil || !got {
			t.Fatalf("identity %s: %t %v", typ.Key(), got, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&builder{ctx: ctx}).normalizationPreservesJSON(i, i); err == nil {
		t.Fatal("cached or exact proof ignored cancellation")
	}
}
