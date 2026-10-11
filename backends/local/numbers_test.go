package local

import (
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"math"
	"math/big"
	"math/rand"
	"testing"
)

func TestExactMixedComparisonAgainstRational(t *testing.T) {
	random := rand.New(rand.NewSource(17))
	for i := 0; i < 10000; i++ {
		integer := int64(random.Uint64())
		float := math.Float64frombits(random.Uint64())
		if math.IsNaN(float) || math.IsInf(float, 0) {
			continue
		}
		expected := new(big.Rat).SetInt64(integer).Cmp(new(big.Rat).SetFloat64(float))
		if got := compareIntFloat(integer, float); got != expected {
			t.Fatalf("%d vs %g: got %d want %d", integer, float, got, expected)
		}
	}
}
func TestIntegerOverflowAgainstBigIntegers(t *testing.T) {
	random := rand.New(rand.NewSource(31))
	for i := 0; i < 10000; i++ {
		x, y := int64(random.Uint64()), int64(random.Uint64())
		for _, op := range []plan.Operator{plan.Add, plan.Subtract, plan.Multiply} {
			a, b := big.NewInt(x), big.NewInt(y)
			want := new(big.Int)
			switch op {
			case plan.Add:
				want.Add(a, b)
			case plan.Subtract:
				want.Sub(a, b)
			case plan.Multiply:
				want.Mul(a, b)
			}
			got := arithmetic(op, core.Int(x), core.Int(y))
			if !want.IsInt64() {
				if got.Kind() != core.NullKind {
					t.Fatalf("overflow %d%s%d: %v", x, op, y, got)
				}
			} else {
				actual, ok := got.AsInt()
				if !ok || actual != want.Int64() {
					t.Fatalf("%d%s%d: got %d want %s", x, op, y, actual, want)
				}
			}
		}
	}
}
