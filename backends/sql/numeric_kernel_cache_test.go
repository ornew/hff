package sql

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func TestMixedNumericOrderKeepsPresenceOutsideCache(t *testing.T) {
	b := &builder{ctx: context.Background()}
	i := expression{"i", "left_present", hff.IntType}
	f := expression{"f", "right_present", hff.FloatType}
	first := b.mixedComparison(i, f, hff.Less)
	i.present, f.present = "other_left", "other_right"
	reversed := b.mixedComparison(f, i, hff.GreaterEqual)
	if len(b.steps) != 2 {
		t.Fatal("same typed pair was projected more than once", b.steps)
	}
	if !strings.Contains(first.code, "left_present") || !strings.Contains(first.code, "right_present") ||
		!strings.Contains(reversed.code, "other_left") || !strings.Contains(reversed.code, "other_right") ||
		strings.Contains(reversed.code, "left_present") {
		t.Fatal("comparison presence leaked through the cached order", first, reversed)
	}
	b.mixedComparison(expression{"j", "TRUE", hff.IntType}, f, hff.Equal)
	if len(b.steps) != 3 {
		t.Fatal("a different Int did not retain its own order with the shared Float floor", b.steps)
	}
	// Scope restoration must be able to discard this cache without carrying
	// its projected aliases into a different row source.
	b.steps, b.sharedPaths = nil, nil
	b.mixedComparison(i, f, hff.Less)
	if len(b.steps) != 2 {
		t.Fatal("new scope reused an unavailable numeric projection", b.steps)
	}
}

func TestJSONNumericFamiliesKeepGuardAndOverrideIdentity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		left, right jsonNumericSide
		lf, rf      string
		want        []string
	}{
		{"different subtype rules", jsonNumericSide{true, true, true}, jsonNumericSide{false, true, false}, "", "", []string{"THEN", "double precision"}},
		{"different Float carriers", jsonNumericSide{true, true, true}, jsonNumericSide{true, true, true}, "left_float", "right_float", []string{"left_float", "right_float"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := &builder{ctx: context.Background()}
			code := b.decodedJSONNumericPair("same_text", "same_text", hff.Equal, tc.left, tc.right, tc.lf, tc.rf)
			for _, want := range tc.want {
				if !strings.Contains(code, want) {
					t.Fatal("a distinct family guard or Float carrier was lost", code)
				}
			}
			if len(b.steps) != 0 || len(b.sharedPaths) != 0 {
				t.Fatal("pair-local numeric kernels leaked into the outer scope")
			}
		})
	}
}
