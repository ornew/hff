package conformance

import (
	"math"
	"strings"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

// AggregateProfileCase describes both declared accumulation models explicitly.
// PostgreSQLSource compares the result with expected; LocalSource uses
// local_expected. Neither expectation is inferred by running the other backend.
type AggregateProfileCase struct {
	Name             string
	Element          hff.Type
	Schema           hff.Type
	Record           hff.Value
	LocalSource      string
	PostgreSQLSource string
	LocalMatch       bool // Local's match for PostgreSQLSource can intentionally differ.
}

// FloatAggregateCases returns finite boundary inputs and ordinary/failure cases
// for sum and avg, including direct, guarded, structured and captured callbacks.
func FloatAggregateCases() []AggregateProfileCase {
	number := schema.Union(schema.IntType, schema.FloatType, schema.NullType)
	float := schema.NullableType(schema.FloatType)
	type fixture struct {
		name             string
		element          hff.Type
		input            hff.Value
		localSum, sqlSum hff.Value
		localAvg, sqlAvg hff.Value
	}
	fixtures := []fixture{
		{"ordinary", float, hff.List(hff.Float(1.25), hff.Null(), hff.Float(2.5)), hff.Float(3.75), hff.Float(3.75), hff.Float(1.875), hff.Float(1.875)},
		{"cancellation", float, hff.List(hff.Float(1e16), hff.Float(1), hff.Float(-1e16)), hff.Float(0), hff.Float(1), hff.Float(0), hff.Float(1.0 / 3)},
		{"intermediate_overflow", float, hff.List(hff.Float(math.MaxFloat64), hff.Float(math.MaxFloat64), hff.Float(-math.MaxFloat64)), hff.Null(), hff.Float(math.MaxFloat64), hff.Null(), hff.Float(math.MaxFloat64 / 3)},
		{"final_sum_overflow", float, hff.List(hff.Float(math.MaxFloat64), hff.Float(math.MaxFloat64)), hff.Null(), hff.Null(), hff.Null(), hff.Float(math.MaxFloat64)},
		{"large_pair", float, hff.List(hff.Float(1e308), hff.Float(1e308)), hff.Null(), hff.Null(), hff.Null(), hff.Float(1e308)},
		{"signed_zero", float, hff.List(hff.Float(math.Copysign(0, -1))), hff.Float(0), hff.Float(0), hff.Float(0), hff.Float(0)},
		{"subnormal_pair", float, hff.List(hff.Float(math.SmallestNonzeroFloat64), hff.Float(math.SmallestNonzeroFloat64)), hff.Float(2 * math.SmallestNonzeroFloat64), hff.Float(2 * math.SmallestNonzeroFloat64), hff.Float(math.SmallestNonzeroFloat64), hff.Float(math.SmallestNonzeroFloat64)},
		{"empty", float, hff.List(), hff.Int(0), hff.Int(0), hff.Null(), hff.Null()},
		{"all_null", float, hff.List(hff.Null(), hff.Null()), hff.Int(0), hff.Int(0), hff.Null(), hff.Null()},
		{"null_receiver", float, hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null()},
		{"mixed_precision", number, hff.List(hff.Int(9007199254740993), hff.Float(-9007199254740992)), hff.Float(0), hff.Float(1), hff.Float(0), hff.Float(0.5)},
		{"integer_prefix_then_float", number, hff.List(hff.Int(math.MaxInt64), hff.Int(1), hff.Int(-math.MaxInt64), hff.Float(0)), hff.Float(1), hff.Float(1), hff.Float(0.25), hff.Float(0.25)},
		{"integer_cancellation", schema.NullableType(schema.IntType), hff.List(hff.Int(math.MaxInt64), hff.Int(1), hff.Int(-1)), hff.Int(math.MaxInt64), hff.Int(math.MaxInt64), hff.Float(float64(math.MaxInt64) / 3), hff.Float(float64(math.MaxInt64) / 3)},
		{"integer_overflow", schema.NullableType(schema.IntType), hff.List(hff.Int(math.MaxInt64), hff.Int(1)), hff.Null(), hff.Null(), hff.Float(1 << 62), hff.Float(1 << 62)},
		{"invalid_runtime_type", schema.AnyType, hff.List(hff.Float(1), hff.String("invalid")), hff.Null(), hff.Null(), hff.Null(), hff.Null()},
	}
	var cases []AggregateProfileCase
	add := func(name string, element hff.Type, input hff.Value, expression string, local, postgres hff.Value) {
		cases = append(cases, AggregateProfileCase{
			Name: name, Element: element,
			Schema: schema.Struct(schema.Nullable(schema.List("items", element)),
				schema.Field("expected", number), schema.Field("local_expected", number), schema.Float("rate")),
			Record: hff.Object(map[string]hff.Value{
				"items": input, "expected": postgres, "local_expected": local, "rate": hff.Float(1.25),
			}),
			LocalSource:      aggregatePredicate(expression, "local_expected", local),
			PostgreSQLSource: aggregatePredicate(expression, "expected", postgres),
			LocalMatch:       sameAggregateResult(local, postgres),
		})
	}
	for _, f := range fixtures {
		frames := []struct {
			name string
			typ  hff.Type
		}{{"typed", f.element}}
		if f.element.Kind() != hff.AnyType {
			frames = append(frames, struct {
				name string
				typ  hff.Type
			}{"dynamic", schema.AnyType})
		}
		for _, frame := range frames {
			for _, aggregate := range []struct {
				name            string
				local, postgres hff.Value
			}{{"sum", f.localSum, f.sqlSum}, {"avg", f.localAvg, f.sqlAvg}} {
				for _, call := range []string{"", "(v=>v)", "(v=>v!)"} {
					add(f.name+"/"+frame.name+"/"+aggregate.name+call, frame.typ, f.input,
						"items:"+aggregate.name+call, aggregate.local, aggregate.postgres)
				}
			}
		}
		// A dynamic price projection retains original subtypes. A missing
		// price and a null price are both excluded from numeric aggregation.
		if values, list := f.input.AsList(); list {
			items := make([]hff.Value, 0, len(values)+1)
			for _, value := range values {
				items = append(items, hff.Object(map[string]hff.Value{"price": value}))
			}
			items = append(items, hff.Object(nil))
			for _, aggregate := range []struct {
				name            string
				local, postgres hff.Value
			}{{"sum", f.localSum, f.sqlSum}, {"avg", f.localAvg, f.sqlAvg}} {
				for _, guard := range []string{"", "!"} {
					add(f.name+"/projection/"+aggregate.name+guard, schema.MapOf(schema.AnyType), hff.List(items...),
						"items:"+aggregate.name+"(v=>v.price"+guard+")", aggregate.local, aggregate.postgres)
				}
			}
		}
	}
	input := hff.List(hff.Null(), hff.Float(2))
	add("captured_rate/sum", float, input, "items:sum(v=>rate)", hff.Float(2.5), hff.Float(2.5))
	add("captured_rate/avg", float, input, "items:avg(v=>rate)", hff.Float(1.25), hff.Float(1.25))
	// Check mixed comparison after the aggregate rounds to Float: the adjacent
	// integer must not become equal merely because it was coerced to Float.
	for _, element := range []hff.Type{number, schema.AnyType} {
		add("rounded_sum_mixed_comparison/"+element.Key(), element,
			hff.List(hff.Int(9007199254740993), hff.Float(0)), "items:sum", hff.Float(9007199254740992), hff.Float(9007199254740992))
		c := &cases[len(cases)-1]
		c.PostgreSQLSource = `items:sum!=9007199254740993 items:sum:[float]:exists`
		c.LocalSource = c.PostgreSQLSource
		c.LocalMatch = true
	}
	// Exercise the exact filters used to explain the profiles in the guide.
	for _, c := range cases {
		if c.Name == "cancellation/typed/sum" {
			c.Name, c.PostgreSQLSource, c.LocalMatch = "guide_sum_positive", "items:sum>0", false
			cases = append(cases, c)
		} else if c.Name == "large_pair/typed/avg" {
			c.Name, c.PostgreSQLSource, c.LocalMatch = "guide_average_exists", "items:avg:exists", false
			cases = append(cases, c)
		}
	}
	return cases
}

func aggregatePredicate(expression, field string, want hff.Value) string {
	if want.Kind() == hff.NullKind {
		return expression + "=null"
	}
	typ := "float"
	if want.Kind() == hff.IntKind {
		typ = "int"
	}
	predicate := expression + "=" + field + " " + expression + ":[" + typ + "]:exists"
	if value, floating := want.AsFloat(); floating && value == 0 {
		// Numeric equality does not observe zero's sign. Pin the model's
		// declared zero result separately through its public string form.
		zero := "0.0"
		if math.Signbit(value) {
			zero = "-0.0"
		}
		predicate += " " + expression + ":string=\"" + zero + "\""
	}
	return predicate
}

func sameAggregateResult(left, right hff.Value) bool {
	if left.Kind() != right.Kind() {
		return false
	}
	switch left.Kind() {
	case hff.NullKind:
		return true
	case hff.IntKind:
		l, _ := left.AsInt()
		r, _ := right.AsInt()
		return l == r
	case hff.FloatKind:
		l, _ := left.AsFloat()
		r, _ := right.AsFloat()
		return l == r
	}
	return false
}

// ScalarFloatAverageCases selects cases usable with native Float relation
// elements. Projection and mixed-typed items have their own JSON coverage.
func ScalarFloatAverageCases() []AggregateProfileCase {
	var out []AggregateProfileCase
	for _, c := range FloatAggregateCases() {
		if c.Element.Key() == schema.NullableType(schema.FloatType).Key() && strings.HasPrefix(c.PostgreSQLSource, "items:avg") {
			out = append(out, c)
		}
	}
	return out
}
