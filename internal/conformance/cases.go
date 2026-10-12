// Package conformance holds shared language examples for parser, compiler, and
// backend tests. Expectations describe observable semantics, not IR shapes.
package conformance

import (
	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/schema"
)

type Record struct {
	Name  string
	Value hff.Value
	Match bool
	Error hff.ErrorCode
}

type Case struct {
	Name         string
	Source       string
	Schema       hff.Type
	Capabilities capabilities.Set
	Limits       hff.Limits
	ParseError   hff.ErrorCode
	CompileError hff.ErrorCode
	Records      []Record
}

func record(name string, fields map[string]hff.Value, match bool) Record {
	values := map[string]hff.Value{
		"age": hff.Int(0), "name": hff.String(""), "flag": hff.Bool(false),
		"x": hff.Null(), "y": hff.Null(), "items": hff.List(),
		"labels": hff.Object(map[string]hff.Value{}),
	}
	for key, value := range fields {
		values[key] = value
	}
	return Record{Name: name, Value: hff.Object(values), Match: match}
}

// Cases returns fresh test configuration, with immutable HFF schemas and values.
func Cases() []Case {
	common := schema.Struct(schema.Int("age"), schema.String("name"), schema.Bool("flag"),
		schema.Any("x"), schema.Any("y"), schema.List("items", schema.AnyType), schema.Map("labels", schema.AnyType))
	nullableFields := common.Fields()
	for i := range nullableFields {
		if nullableFields[i].Name == "age" {
			nullableFields[i] = schema.Nullable(nullableFields[i])
		}
	}
	nullableAge := schema.Struct(nullableFields...)
	budgetedItems := hff.List(hff.Int(1), hff.Int(2), hff.Int(3), hff.Int(4), hff.Int(5), hff.Int(6), hff.Int(7), hff.Int(8))
	cases := []Case{
		{Name: "nullable_guards_keep_presence", Source: `age!:[int]!=null`, Schema: nullableAge, Records: []Record{
			record("null", map[string]hff.Value{"age": hff.Null()}, false), record("zero", nil, true),
			record("positive", map[string]hff.Value{"age": hff.Int(18)}, true),
		}},
		{Name: "nonnull_guards_preserve_zero", Source: `age!:[int]!=null`, Records: []Record{
			record("zero", nil, true), record("positive", map[string]hff.Value{"age": hff.Int(18)}, true),
			record("negative", map[string]hff.Value{"age": hff.Int(-1)}, true),
		}},
		{Name: "nonnull_null_test_is_false", Source: `age=null | flag`, Records: []Record{
			record("zero_false", nil, false), record("zero_true", map[string]hff.Value{"flag": hff.Bool(true)}, true),
			record("positive_false", map[string]hff.Value{"age": hff.Int(18)}, false),
		}},
		{Name: "json_preserves_float", Source: `x:json:fromjson:[float]=1`, Records: []Record{
			record("float", map[string]hff.Value{"x": hff.Float(1)}, true),
			record("int", map[string]hff.Value{"x": hff.Int(1)}, false),
		}},
		{Name: "json_duplicate_keys_fail", Source: `x:fromjson=null`, Records: []Record{
			record("duplicate", map[string]hff.Value{"x": hff.String(`{"a":1,"\u0061":2}`)}, true),
			record("valid", map[string]hff.Value{"x": hff.String(`{"a":1}`)}, false),
		}},
		{Name: "json_null_is_observable", Source: `x:json="null" x:string="null"`, Records: []Record{
			record("null", nil, true), record("integer", map[string]hff.Value{"x": hff.Int(1)}, false),
		}},
		{Name: "json_guard_stays_absent", Source: `x!:json:exists`, Records: []Record{
			record("null", nil, false), record("zero", map[string]hff.Value{"x": hff.Int(0)}, true),
		}},
		{Name: "empty_filter", Source: " \t\n", Records: []Record{record("default", nil, true)}},
		{Name: "and_before_or", Source: `age>=10 | age<=5 name="Alice"`, Records: []Record{
			record("or_left", map[string]hff.Value{"age": hff.Int(10), "name": hff.String("Bob")}, true),
			record("right_conjunction", map[string]hff.Value{"age": hff.Int(5), "name": hff.String("Alice")}, true),
			record("right_name_missing", map[string]hff.Value{"age": hff.Int(5), "name": hff.String("Bob")}, false),
		}},
		{Name: "grouped_or", Source: `(age>=10 | age<=5) name="Alice"`, Records: []Record{
			record("name_required", map[string]hff.Value{"age": hff.Int(10), "name": hff.String("Bob")}, false),
			record("matches", map[string]hff.Value{"age": hff.Int(10), "name": hff.String("Alice")}, true),
		}},
		{Name: "implicit_truthiness", Source: `age name`, Records: []Record{
			record("zero", map[string]hff.Value{"name": hff.String("Alice")}, false),
			record("empty_string", map[string]hff.Value{"age": hff.Int(18)}, false),
			record("both_present", map[string]hff.Value{"age": hff.Int(18), "name": hff.String("Alice")}, true),
		}},
		{Name: "not_encloses_comparison", Source: `!x="Female"`, Records: []Record{
			record("null", nil, true),
			record("equal", map[string]hff.Value{"x": hff.String("Female")}, false),
			record("different", map[string]hff.Value{"x": hff.String("Male")}, true),
		}},
		{Name: "null_inequality_is_false", Source: `x!="Female"`, Records: []Record{
			record("null", nil, false),
			record("different", map[string]hff.Value{"x": hff.String("Male")}, true),
		}},
		{Name: "explicit_null_test", Source: `x=null`, Records: []Record{
			record("null", nil, true), record("zero", map[string]hff.Value{"x": hff.Int(0)}, false),
		}},
		{Name: "two_null_values_are_not_equal", Source: `x=y`, Records: []Record{record("both_null", nil, false)}},
		{Name: "not_of_null_comparison", Source: `!(x=y)`, Records: []Record{record("both_null", nil, true)}},
		{Name: "type_guard_failure_is_not_null", Source: `x:[string]=null`, Records: []Record{
			record("wrong_type", map[string]hff.Value{"x": hff.Int(1)}, false),
		}},
		{Name: "exists_observes_failed_type_guard", Source: `x:[string]:exists`, Records: []Record{
			record("wrong_type", map[string]hff.Value{"x": hff.Int(1)}, false),
		}},
		{Name: "guard_failure_is_not_null", Source: `x!.value=null`, Records: []Record{
			record("missing_parent", nil, false),
			record("null_field", map[string]hff.Value{"x": hff.Object(map[string]hff.Value{"value": hff.Null()})}, true),
			record("missing_field", map[string]hff.Value{"x": hff.Object(map[string]hff.Value{})}, true),
		}},
		{Name: "not_inverts_failed_guard", Source: `!(x!.value=null)`, Records: []Record{
			record("missing_parent", nil, true),
			record("null_field", map[string]hff.Value{"x": hff.Object(map[string]hff.Value{"value": hff.Null()})}, false),
		}},
		{Name: "exists_observes_falsy_values", Source: `x:exists`, Records: []Record{
			record("null", nil, false), record("false", map[string]hff.Value{"x": hff.Bool(false)}, true),
			record("zero", map[string]hff.Value{"x": hff.Int(0)}, true),
			record("empty_list", map[string]hff.Value{"x": hff.List()}, true),
		}},
		{Name: "explicit_type_guard", Source: `x:[string]:contains("api")`, Records: []Record{
			record("string", map[string]hff.Value{"x": hff.String("api-server")}, true),
			record("list", map[string]hff.Value{"x": hff.List(hff.String("api"))}, false),
			record("null", nil, false),
		}},
		{Name: "exact_mixed_number_comparison", Source: `x>y`, Records: []Record{
			record("integer_above_rounded_float", map[string]hff.Value{"x": hff.Int(9007199254740993), "y": hff.Float(9007199254740992)}, true),
			record("equal_values", map[string]hff.Value{"x": hff.Int(9007199254740992), "y": hff.Float(9007199254740992)}, false),
		}},
		{Name: "fractional_integer_division", Source: `age/2=2.5`, Records: []Record{
			record("five", map[string]hff.Value{"age": hff.Int(5)}, true),
			record("four", map[string]hff.Value{"age": hff.Int(4)}, false),
		}},
		{Name: "overflow_is_null", Source: `(age+1)=null`, Records: []Record{
			record("overflow", map[string]hff.Value{"age": hff.Int(9223372036854775807)}, true),
			record("finite", map[string]hff.Value{"age": hff.Int(1)}, false),
		}},
		{Name: "negative_index", Source: `items[-1]=2`, Records: []Record{
			record("last_item", map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Int(2))}, true),
			record("empty", nil, false),
		}},
		{Name: "contains_requires_all_arguments", Source: `items:contains(null,2)`, Records: []Record{
			record("both", map[string]hff.Value{"items": hff.List(hff.Null(), hff.Int(2))}, true),
			record("one", map[string]hff.Value{"items": hff.List(hff.Int(2))}, false),
		}},
		{Name: "structural_equality_ignores_key_order", Source: `x={"a":null,"b":[1,2.0]}`, Records: []Record{
			record("equal", map[string]hff.Value{"x": hff.Object(map[string]hff.Value{"b": hff.List(hff.Float(1), hff.Int(2)), "a": hff.Null()})}, true),
			record("missing_is_distinct", map[string]hff.Value{"x": hff.Object(map[string]hff.Value{"b": hff.List(hff.Int(1), hff.Int(2))})}, false),
		}},
		{Name: "unique_is_a_predicate", Source: `items:unique`, Records: []Record{
			record("empty", nil, true),
			record("mixed_numeric_duplicate", map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Float(1))}, false),
		}},
		{Name: "any_and_all_empty_lists", Source: `!items:any(v=>v) items:all(v=>v)`, Records: []Record{record("empty", nil, true)}},
		{Name: "callback_predicate_guard", Source: `items:any(v=>v!.active)`, Records: []Record{
			record("failed_guard_then_match", map[string]hff.Value{"items": hff.List(hff.Null(), hff.Object(map[string]hff.Value{"active": hff.Bool(true)}))}, true),
		}},
		{Name: "nested_callback_captures", Source: `items:any(v=>[v,age]:all(w=>w>10))`, Records: []Record{
			record("all_values", map[string]hff.Value{"age": hff.Int(20), "items": hff.List(hff.Int(1), hff.Int(11))}, true),
			record("captured_age_fails", map[string]hff.Value{"age": hff.Int(5), "items": hff.List(hff.Int(11))}, false),
		}},
		{Name: "fold_argument_order", Source: `items:foldl(0,(acc,v)=>acc-v)=-6 items:foldr(0,(v,acc)=>v-acc)=2`, Records: []Record{
			record("right_fold", map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Int(2), hff.Int(3))}, true),
		}},
		{Name: "regex_partial_match", Source: `name~"Alice"`, Records: []Record{
			record("substring", map[string]hff.Value{"name": hff.String("Hello Alice!")}, true),
		}},
		{Name: "invalid_regex_does_not_match_negation", Source: `name!~"["`, Records: []Record{record("invalid_pattern", map[string]hff.Value{"name": hff.String("Alice")}, false)}},
		{Name: "unicode_code_points", Source: `name:len=2`, Records: []Record{record("combining_mark", map[string]hff.Value{"name": hff.String("e\u0301")}, true)}},
		{Name: "null_type_is_observable", Source: `x:type="null"`, Records: []Record{record("null", nil, true)}},
		{Name: "strict_base64", Source: `name:frombase64:len=4`, Records: []Record{
			record("padded", map[string]hff.Value{"name": hff.String("AQIDBA==")}, true),
			record("missing_padding", map[string]hff.Value{"name": hff.String("AQIDBA")}, false),
			record("newline", map[string]hff.Value{"name": hff.String("AQIDBA==\n")}, false),
		}},
		{Name: "duration_compound_fraction", Source: `name:duration:nanoseconds=5400500000000`, Records: []Record{
			record("compound", map[string]hff.Value{"name": hff.String("1h30m.5s")}, true),
			record("sub_nanosecond", map[string]hff.Value{"name": hff.String("0.1ns")}, false),
		}},
		{Name: "timestamp_utc_components", Source: `name:timestamp:year=2026 name:timestamp:hours("+09:00")=18`, Records: []Record{
			record("valid", map[string]hff.Value{"name": hff.String("2026-10-11T09:30:00Z")}, true),
			record("leap_second", map[string]hff.Value{"name": hff.String("2026-10-11T09:30:60Z")}, false),
		}},
		{Name: "epoch_is_truthy", Source: `name:timestamp`, Records: []Record{
			record("epoch", map[string]hff.Value{"name": hff.String("1970-01-01T00:00:00Z")}, true),
		}},
		{Name: "zero_duration_is_falsy", Source: `name:duration`, Records: []Record{
			record("zero", map[string]hff.Value{"name": hff.String("0")}, false),
			record("negative", map[string]hff.Value{"name": hff.String("-1ns")}, true),
		}},
		{Name: "compact_operator_required", Source: `age > 10`, ParseError: hff.SyntaxError, CompileError: hff.SyntaxError},
		{Name: "map_preserves_null_and_order", Source: `items:map(v=>v+1)=[2,null]`, Records: []Record{
			record("matches", map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Null())}, true),
			record("reversed", map[string]hff.Value{"items": hff.List(hff.Null(), hff.Int(1))}, false),
			record("empty", nil, false),
		}},
		{Name: "flatmap_preserves_order", Source: `items:flatmap(v=>[v,v])=[1,1,2,2]`, Records: []Record{
			record("matches", map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Int(2))}, true),
			record("reversed", map[string]hff.Value{"items": hff.List(hff.Int(2), hff.Int(1))}, false),
			record("empty", nil, false),
		}},
		{Name: "sort_mixed_numbers_and_reject_null", Source: `items:sort=[1,2,3]`, Records: []Record{
			record("mixed_numbers", map[string]hff.Value{"items": hff.List(hff.Int(3), hff.Float(1), hff.Int(2))}, true),
			record("null_element", map[string]hff.Value{"items": hff.List(hff.Int(3), hff.Null(), hff.Int(1))}, false),
			record("empty", nil, false),
		}},
		{Name: "concat_preserves_order", Source: `items:concat([3])=[1,2,3]`, Records: []Record{
			record("matches", map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Int(2))}, true),
			record("reversed", map[string]hff.Value{"items": hff.List(hff.Int(2), hff.Int(1))}, false),
			record("empty", nil, false),
		}},
		{Name: "object_lookup_distinguishes_missing", Source: `labels:has("a") labels:get("a")=null`, Records: []Record{
			record("present_null", map[string]hff.Value{"labels": hff.Object(map[string]hff.Value{"a": hff.Null()})}, true),
			record("missing", nil, false),
			record("present_zero", map[string]hff.Value{"labels": hff.Object(map[string]hff.Value{"a": hff.Int(0)})}, false),
		}},
		{Name: "object_dynamic_key_lookup", Source: `labels[name]=1`, Records: []Record{
			record("matches", map[string]hff.Value{"name": hff.String("é"), "labels": hff.Object(map[string]hff.Value{"é": hff.Int(1)})}, true),
			record("not_normalized", map[string]hff.Value{"name": hff.String("e\u0301"), "labels": hff.Object(map[string]hff.Value{"é": hff.Int(1)})}, false),
			record("missing", nil, false),
		}},
		{Name: "object_enumeration_key_order", Source: `labels:keys=["a","b"] labels:values=[1,null] labels:items=[["a",1],["b",null]]`, Records: []Record{
			record("matches", map[string]hff.Value{"labels": hff.Object(map[string]hff.Value{"b": hff.Null(), "a": hff.Int(1)})}, true),
			record("different_values", map[string]hff.Value{"labels": hff.Object(map[string]hff.Value{"a": hff.Null(), "b": hff.Int(1)})}, false),
			record("empty", nil, false),
		}},
		{Name: "decisive_or_skips_budgeted_callback", Source: `flag | items:map(v=>v+1):size>0`, Limits: hff.Limits{MaxSteps: 16}, Records: []Record{
			record("decisive", map[string]hff.Value{"flag": hff.Bool(true), "items": budgetedItems}, true),
			{Name: "evaluated_branch_aborts", Value: record("", map[string]hff.Value{"items": budgetedItems}, false).Value, Error: hff.LimitError},
		}},
		{Name: "decisive_and_skips_budgeted_callback", Source: `flag items:map(v=>v+1):size>0`, Limits: hff.Limits{MaxSteps: 16}, Records: []Record{
			record("decisive", map[string]hff.Value{"items": budgetedItems}, false),
			{Name: "evaluated_branch_aborts", Value: record("", map[string]hff.Value{"flag": hff.Bool(true), "items": budgetedItems}, false).Value, Error: hff.LimitError},
		}},
		{Name: "comparisons_do_not_chain", Source: `age<10<20`, ParseError: hff.SyntaxError, CompileError: hff.SyntaxError},
		{Name: "constant_left_comparison_rejected", Source: `10<age`, CompileError: hff.TypeError},
		{Name: "known_type_rejects_method", Source: `age:contains("x")`, CompileError: hff.TypeError},
		{Name: "disabled_structural_equality", Source: `items=[]`, CompileError: hff.CapabilityError,
			Capabilities: capabilities.Standard().Without(capabilities.ListEquality)},
		{Name: "disabled_callback_nesting", Source: `items:any(v=>[v]:all(w=>w>0))`, CompileError: hff.CapabilityError,
			Capabilities: capabilities.Standard().Without(capabilities.NestedCallbacks)},
		{Name: "type_guard_limits_method_permissions", Source: `x:[string]:contains("api")`,
			Capabilities: capabilities.Standard().Without(capabilities.Method("list.contains")),
			Records: []Record{
				record("string_allowed", map[string]hff.Value{"x": hff.String("api-server")}, true),
				record("list_guard_rejected", map[string]hff.Value{"x": hff.List(hff.String("api"))}, false),
			}},
		{Name: "invalid_host_input_is_rejected", Source: `age>=18`, Records: []Record{{
			Name: "wrong_age_type", Value: record("", map[string]hff.Value{"age": hff.String("18")}, false).Value,
			Error: hff.InputError,
		}}},
	}
	for i := range cases {
		if cases[i].Schema.Kind() == hff.InvalidType {
			cases[i].Schema = common
		}
		if !cases[i].Capabilities.Specified() {
			cases[i].Capabilities = capabilities.Standard()
		}
	}
	return append(cases, CompositeCases()...)
}
