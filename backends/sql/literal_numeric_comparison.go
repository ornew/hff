package sql

import (
	"math"
	"math/big"

	"github.com/ornew/hff"
)

type literalComparisonResult uint8

const (
	literalComparison literalComparisonResult = iota
	literalFalse
	literalNonNull
)

type literalNumericComparison struct {
	result literalComparisonResult
	op     hff.Operator
	value  hff.Value
}

// mixedLiteralComparison replaces exact mixed-number comparisons with a
// same-type threshold or a two-valued null test. All rounding happens while
// building SQL; no Int is rounded to Float for comparison at execution time.
func mixedLiteralComparison(kind hff.TypeKind, op hff.Operator, value hff.Value) (literalNumericComparison, bool) {
	switch op {
	case hff.Equal, hff.NotEqual, hff.Less, hff.LessEqual, hff.Greater, hff.GreaterEqual:
	default:
		return literalNumericComparison{}, false
	}
	if kind == hff.IntType {
		f, ok := value.AsFloat()
		if !ok || math.IsInf(f, 0) || math.IsNaN(f) {
			return literalNumericComparison{}, false
		}
		return integerFloatLiteralComparison(op, f), true
	}
	if kind != hff.FloatType {
		return literalNumericComparison{}, false
	}
	k, ok := value.AsInt()
	if !ok {
		return literalNumericComparison{}, false
	}
	f := float64(k)
	// Compare exact represented values, including the rounded 2^63 value of
	// float64(MaxInt64). Converting that Float back to int64 would overflow.
	order := big.NewFloat(f).Cmp(new(big.Float).SetInt64(k))
	if order == 0 {
		return literalNumericComparison{op: op, value: hff.Float(f)}, true
	}
	if op == hff.Equal {
		return literalNumericComparison{result: literalFalse}, true
	}
	if op == hff.NotEqual {
		return literalNumericComparison{result: literalNonNull}, true
	}
	low, high := f, math.Nextafter(f, math.Inf(1))
	if order > 0 {
		low, high = math.Nextafter(f, math.Inf(-1)), f
	}
	if op == hff.Less || op == hff.LessEqual {
		return literalNumericComparison{op: hff.LessEqual, value: hff.Float(low)}, true
	}
	return literalNumericComparison{op: hff.GreaterEqual, value: hff.Float(high)}, true
}

func integerFloatLiteralComparison(op hff.Operator, f float64) literalNumericComparison {
	truncated, accuracy := big.NewFloat(f).Int(nil)
	floor, ceil := new(big.Int).Set(truncated), new(big.Int).Set(truncated)
	if accuracy == big.Above {
		floor.Sub(floor, big.NewInt(1))
	} else if accuracy == big.Below {
		ceil.Add(ceil, big.NewInt(1))
	}
	threshold := floor
	switch op {
	case hff.Equal, hff.NotEqual:
		if accuracy != big.Exact || !threshold.IsInt64() {
			result := literalFalse
			if op == hff.NotEqual {
				result = literalNonNull
			}
			return literalNumericComparison{result: result}
		}
	case hff.Less:
		threshold = ceil.Sub(ceil, big.NewInt(1))
		op = hff.LessEqual
	case hff.LessEqual:
	case hff.Greater:
		threshold = floor.Add(floor, big.NewInt(1))
		op = hff.GreaterEqual
	case hff.GreaterEqual:
		threshold = ceil
	}
	minimum, maximum := big.NewInt(math.MinInt64), big.NewInt(math.MaxInt64)
	if op == hff.LessEqual {
		if threshold.Cmp(minimum) < 0 {
			return literalNumericComparison{result: literalFalse}
		}
		if threshold.Cmp(maximum) >= 0 {
			return literalNumericComparison{result: literalNonNull}
		}
	} else if op == hff.GreaterEqual {
		if threshold.Cmp(maximum) > 0 {
			return literalNumericComparison{result: literalFalse}
		}
		if threshold.Cmp(minimum) <= 0 {
			return literalNumericComparison{result: literalNonNull}
		}
	}
	return literalNumericComparison{op: op, value: hff.Int(threshold.Int64())}
}

func (b *builder) directMixedLiteralComparison(left, right hff.Node, column string, plan literalNumericComparison) (string, error) {
	if plan.result == literalFalse {
		return "FALSE", nil
	}
	if plan.result == literalNonNull {
		return "(" + column + " IS NOT NULL)", nil
	}
	value, err := b.parameter(right, plan.value)
	if err != nil {
		return "", err
	}
	operator := string(plan.op)
	if plan.op == hff.NotEqual {
		operator = "<>"
	}
	test := column + " " + operator + " " + value.code
	if acceptsKind(left.Type(), hff.NullType) {
		test = column + " IS NOT NULL AND " + test
	}
	return "(" + test + ")", nil
}
