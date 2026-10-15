package sql

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func TestUnconstrainedItemValueGuardOmitsKeyDispatch(t *testing.T) {
	for _, typ := range []hff.Type{
		hff.NewStructType(hff.Field{Name: "a", Type: hff.PrimitiveType(hff.AnyType)}, hff.Field{Name: "z", Type: hff.PrimitiveType(hff.BytesType)}),
		hff.NewMapType(hff.PrimitiveType(hff.DurationType)),
		hff.PrimitiveType(hff.AnyType),
	} {
		b := &builder{ctx: context.Background()}
		code, err := b.jsonItemValueMatches(hff.Node{}, "pair", JSON, &jsonItemRoles{object: typ}, hff.PrimitiveType(hff.AnyType))
		if err != nil || code != "TRUE" || len(b.args) != 0 {
			t.Fatal(typ.Key(), code, b.args, err)
		}
	}
}

func TestUnconstrainedItemValueGuardStillValidatesKeys(t *testing.T) {
	for _, key := range []string{"bad\x00key", "bad\xffkey"} {
		b := &builder{ctx: context.Background()}
		typ := hff.NewStructType(hff.Field{Name: "valid", Type: hff.PrimitiveType(hff.StringType)}, hff.Field{Name: key, Type: hff.PrimitiveType(hff.BytesType)})
		_, err := b.jsonItemValueMatches(hff.Node{}, "pair", JSON, &jsonItemRoles{object: typ}, hff.PrimitiveType(hff.AnyType))
		if err == nil || !strings.Contains(err.Error(), "PostgreSQL text cannot encode invalid UTF-8 or NUL") {
			t.Fatal(key, err)
		}
		if len(b.args) != 0 {
			t.Fatal("validation bound an unused key", b.args)
		}
	}
}
