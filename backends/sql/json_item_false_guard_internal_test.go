package sql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func TestImpossibleItemValueGuardDropsOnlyConstantDispatch(t *testing.T) {
	bytes, text := hff.PrimitiveType(hff.BytesType), hff.PrimitiveType(hff.StringType)
	fields := hff.NewStructType(hff.Field{Name: "a", Type: bytes}, hff.Field{Name: "z", Type: bytes})
	for _, format := range []JSONFormat{JSON, JSONB} {
		b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
		got, err := b.jsonItemValueMatches(hff.Node{}, "pair", format, &jsonItemRoles{object: fields}, text)
		if err != nil || got != "FALSE" || !reflect.DeepEqual(b.args, []any{"existing"}) || b.alias != 7 || len(b.steps) != 0 {
			t.Fatal(format, got, b.args, b.alias, b.steps, err)
		}
	}
	for _, tc := range []struct {
		name           string
		source, target hff.Type
	}{
		{"mixed fields", hff.NewStructType(hff.Field{Name: "a", Type: text}, hff.Field{Name: "z", Type: bytes}), text},
		{"Any fallback", hff.NewUnionType(hff.PrimitiveType(hff.AnyType), fields), text},
		{"nullable value", hff.NewMapType(hff.NewUnionType(bytes, hff.PrimitiveType(hff.NullType))), hff.PrimitiveType(hff.NullType)},
		{"Float decoding null", hff.NewMapType(hff.PrimitiveType(hff.FloatType)), hff.PrimitiveType(hff.NullType)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []JSONFormat{JSON, JSONB} {
				roles := &jsonItemRoles{object: tc.source}
				ordinary := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
				want, err := ordinary.jsonItemValueOperation(hff.Node{}, "pair", roles, func(source hff.Type) (string, error) {
					return ordinary.jsonMatches(hff.Node{}, "(pair -> 1)", format, source, tc.target)
				})
				if err != nil {
					t.Fatal(err)
				}
				b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
				got, err := b.jsonItemValueMatches(hff.Node{}, "pair", format, roles, tc.target)
				if err != nil || got != want || !reflect.DeepEqual(b.args, ordinary.args) || b.alias != ordinary.alias || !reflect.DeepEqual(b.steps, ordinary.steps) {
					t.Fatal(format, got, want, b.args, ordinary.args, err)
				}
				if got == "FALSE" {
					t.Fatal("data-dependent match collapsed")
				}
			}
		})
	}
}

func TestImpossibleItemValueGuardStillValidatesEveryKey(t *testing.T) {
	bytes, text := hff.PrimitiveType(hff.BytesType), hff.PrimitiveType(hff.StringType)
	for _, key := range []string{"bad\x00key", "bad\xffkey"} {
		b := &builder{ctx: context.Background(), args: []any{"existing"}}
		typ := hff.NewStructType(hff.Field{Name: "valid", Type: bytes}, hff.Field{Name: key, Type: bytes})
		_, err := b.jsonItemValueMatches(hff.Node{}, "pair", JSON, &jsonItemRoles{object: typ}, text)
		if err == nil || !strings.Contains(err.Error(), "PostgreSQL text cannot encode invalid UTF-8 or NUL") {
			t.Fatal(key, err)
		}
		if !reflect.DeepEqual(b.args, []any{"existing"}) {
			t.Fatal("validation leaked key bindings", b.args)
		}
	}
}
