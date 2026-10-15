package sql

import (
	"context"
	"reflect"
	"testing"

	"github.com/ornew/hff"
)

func TestGuardReusesOnlyCompatibleNormalizedWholeValue(t *testing.T) {
	i, f := hff.PrimitiveType(hff.IntType), hff.PrimitiveType(hff.FloatType)
	list := hff.NewListType(i)
	structure := hff.NewStructType(hff.Field{Name: "f", Type: i})
	for _, tc := range []struct {
		name           string
		source, target hff.Type
	}{
		{"exact list", list, list},
		{"widened list", list, hff.NewListType(hff.NewUnionType(i, f))},
		{"struct to map", structure, hff.NewMapType(i)},
		{"nullable container", list, hff.NewUnionType(list, hff.PrimitiveType(hff.NullType))},
		{"Any container", list, hff.NewListType(hff.PrimitiveType(hff.AnyType))},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
			b.rememberNormalizedJSON("input", JSON, tc.source)
			got, err := b.normalizeGuardedJSON(hff.Node{}, "input", "masked", JSON, tc.source, tc.target)
			if err != nil || got != "masked" || !reflect.DeepEqual(b.args, []any{"existing"}) || b.alias != 7 || len(b.steps) != 0 {
				t.Fatal(got, b.args, b.alias, b.steps, err)
			}
			// Keep the producer's exact proof, rather than relabeling it with
			// the guard target or its statically narrowed output type.
			if !b.isNormalizedJSON("masked", JSON, tc.source) {
				t.Fatal("producer proof lost")
			}
			if tc.source.Key() != tc.target.Key() && b.isNormalizedJSON("masked", JSON, tc.target) {
				t.Fatal("producer proof relabeled")
			}
		})
	}
}

func TestGuardNormalizationFallbackMatchesOrdinaryState(t *testing.T) {
	i, f := hff.PrimitiveType(hff.IntType), hff.PrimitiveType(hff.FloatType)
	for _, tc := range []struct {
		name           string
		source, target hff.Type
		format         JSONFormat
		proof          bool
	}{
		{"unproven JSON", hff.NewListType(i), hff.NewListType(i), JSON, false},
		{"raw JSONB", hff.NewListType(i), hff.NewListType(i), JSONB, true},
		{"retyped elements", hff.NewListType(i), hff.NewListType(f), JSON, true},
		{"Any source", hff.NewListType(hff.PrimitiveType(hff.AnyType)), hff.NewListType(i), JSON, true},
		{"struct keys", hff.NewStructType(hff.Field{Name: "f", Type: f}), hff.NewMapType(i), JSON, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &builder{ctx: context.Background(), args: []any{"existing"}, alias: 7}
			if tc.proof {
				b.rememberNormalizedJSON("input", tc.format, tc.source)
			}
			ordinary := b.validationBuilder()
			want, err := ordinary.jsonGuardResult(hff.Node{}, "masked", tc.format, tc.source, tc.target)
			if err != nil {
				t.Fatal(err)
			}
			got, err := b.normalizeGuardedJSON(hff.Node{}, "input", "masked", tc.format, tc.source, tc.target)
			if err != nil || got != want || b.alias != ordinary.alias || !reflect.DeepEqual(b.args, ordinary.args) || !reflect.DeepEqual(b.steps, ordinary.steps) || !reflect.DeepEqual(b.jsonValues, ordinary.jsonValues) {
				t.Fatal(got, want, b.args, ordinary.args, err)
			}
			if b.isNormalizedJSON("masked", tc.format, tc.target) {
				t.Fatal("fallback acquired an unproven output contract")
			}
		})
	}
}

func TestGuardNormalizationProofPreservesValidation(t *testing.T) {
	i := hff.PrimitiveType(hff.IntType)
	bad := hff.NewStructType(hff.Field{Name: "bad\x00key", Type: i})
	b := &builder{ctx: context.Background()}
	b.rememberNormalizedJSON("input", JSON, bad)
	if _, err := b.normalizeGuardedJSON(hff.Node{}, "input", "masked", JSON, bad, hff.NewMapType(i)); err == nil {
		t.Fatal("normalization proof bypassed key validation")
	}
	if len(b.args) != 0 || len(b.steps) != 0 {
		t.Fatal("failed validation leaked SQL state")
	}
	if _, ok := b.normalizedJSONValues["masked"]; ok {
		t.Fatal("failed guard acquired a proof")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b.ctx = ctx
	if _, err := b.normalizeGuardedJSON(hff.Node{}, "input", "masked", JSON, bad, hff.NewMapType(i)); err == nil {
		t.Fatal("cancellation ignored")
	}
}
