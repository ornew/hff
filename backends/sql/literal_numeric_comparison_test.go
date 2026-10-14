package sql

import (
	"math"
	"math/big"
	"testing"

	"github.com/ornew/hff"
)

func TestMixedLiteralThresholdsMatchExactRationalOrdering(t *testing.T) {
	integers := []int64{math.MinInt64, math.MinInt64 + 1, -9007199254740993, -9007199254740992, -19, -1, 0, 1, 18, 19, 9007199254740992, 9007199254740993, math.MaxInt64 - 1, math.MaxInt64}
	floats := []float64{-math.MaxFloat64, math.Nextafter(-9223372036854775808, math.Inf(-1)), -9223372036854775808, math.Nextafter(-9223372036854775808, 0), -9007199254740992, -18.5, -1, -0.5, -math.SmallestNonzeroFloat64, math.Copysign(0, -1), 0, math.SmallestNonzeroFloat64, 0.5, 1, 18.5, math.Nextafter(9007199254740992, math.Inf(1)), math.Nextafter(9223372036854775808, 0), 9223372036854775808, math.MaxFloat64}
	ops := []hff.Operator{hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual}
	rational := func(value hff.Value) *big.Rat {
		if i, ok := value.AsInt(); ok {
			return new(big.Rat).SetInt64(i)
		}
		f, _ := value.AsFloat()
		return new(big.Rat).SetFloat64(f)
	}
	compare := func(order int, op hff.Operator) bool {
		switch op {
		case hff.Equal:
			return order == 0
		case hff.NotEqual:
			return order != 0
		case hff.Less:
			return order < 0
		case hff.LessEqual:
			return order <= 0
		case hff.Greater:
			return order > 0
		default:
			return order >= 0
		}
	}
	check := func(kind hff.TypeKind, input, literal hff.Value, op hff.Operator) {
		t.Helper()
		plan, ok := mixedLiteralComparison(kind, op, literal)
		if !ok {
			t.Fatal("mixed literal was not planned")
		}
		want := compare(rational(input).Cmp(rational(literal)), op)
		got := plan.result == literalNonNull
		if plan.result == literalComparison {
			if plan.value.Kind() != input.Kind() {
				t.Fatal("threshold changed the column's numeric subtype", plan)
			}
			got = compare(rational(input).Cmp(rational(plan.value)), plan.op)
		}
		if got != want {
			t.Fatalf("%v %s %v: plan=%v got%v want%v", input, op, literal, plan, got, want)
		}
	}
	for _, i := range integers {
		for _, f := range floats {
			for _, op := range ops {
				check(hff.IntType, hff.Int(i), hff.Float(f), op)
				check(hff.FloatType, hff.Float(f), hff.Int(i), op)
			}
		}
	}
}

func TestMixedLiteralThresholdsRejectOtherOperations(t *testing.T) {
	for _, tc := range []struct {
		kind    hff.TypeKind
		op      hff.Operator
		literal hff.Value
	}{
		{hff.IntType, hff.Add, hff.Float(1.5)},
		{hff.FloatType, hff.Equal, hff.Float(1.5)},
		{hff.IntType, hff.Equal, hff.Int(1)},
		{hff.StringType, hff.Equal, hff.Int(1)},
		{hff.FloatType, hff.Equal, hff.Null()},
	} {
		if _, ok := mixedLiteralComparison(tc.kind, tc.op, tc.literal); ok {
			t.Fatal("ineligible operation was planned", tc)
		}
	}
}
