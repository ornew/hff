package conformance

import (
	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/schema"
)

// CompositeCases preserves a declared shape for storage backends that cannot
// recover arbitrary dynamic values from native composite or array columns.
func CompositeCases() []Case {
	shape := schema.Struct(schema.Nullable(schema.Field("profile", schema.Struct(
		schema.Nullable(schema.Field("father", schema.Struct(schema.Nullable(schema.String("name"))))),
		schema.Nullable(schema.List("scores", schema.NullableType(schema.IntType))),
	))))
	profile := func(father, scores hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"father": father, "scores": scores})
	}
	child := func(name hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"name": name})
	}
	row := func(name string, value hff.Value, match bool) Record {
		return Record{Name: name, Value: hff.Object(map[string]hff.Value{"profile": value}), Match: match}
	}
	return []Case{
		{Name: "typed_composite_null_identity", Source: `profile=null`, Schema: shape, Capabilities: capabilities.Standard(), Records: []Record{
			row("null_parent", hff.Null(), true),
			row("null_child", profile(hff.Null(), hff.Null()), false),
			row("null_name", profile(child(hff.Null()), hff.Null()), false),
		}},
		{Name: "typed_composite_parent_guard", Source: `profile!.father!.name:exists`, Schema: shape, Capabilities: capabilities.Standard(), Records: []Record{
			row("null_parent", hff.Null(), false),
			row("null_child", profile(hff.Null(), hff.Null()), false),
			row("null_name", profile(child(hff.Null()), hff.Null()), false),
			row("empty_name_exists", profile(child(hff.String("")), hff.Null()), true),
		}},
		{Name: "typed_array_negative_index", Source: `profile.scores[-1]=2`, Schema: shape, Capabilities: capabilities.Standard(), Records: []Record{
			row("null_parent", hff.Null(), false),
			row("null_array", profile(hff.Null(), hff.Null()), false),
			row("empty_array", profile(hff.Null(), hff.List()), false),
			row("last_integer", profile(hff.Null(), hff.List(hff.Null(), hff.Int(2))), true),
			row("last_null", profile(hff.Null(), hff.List(hff.Int(2), hff.Null())), false),
		}},
		{Name: "typed_array_structural_equality", Source: `profile.scores=[null,2]`, Schema: shape, Capabilities: capabilities.Standard(), Records: []Record{
			row("null_parent", hff.Null(), false),
			row("null_array", profile(hff.Null(), hff.Null()), false),
			row("empty_array", profile(hff.Null(), hff.List()), false),
			row("last_integer", profile(hff.Null(), hff.List(hff.Null(), hff.Int(2))), true),
			row("last_null", profile(hff.Null(), hff.List(hff.Int(2), hff.Null())), false),
		}},
		{Name: "typed_array_null_membership", Source: `profile.scores:contains(null,2)`, Schema: shape, Capabilities: capabilities.Standard(), Records: []Record{
			row("null_parent", hff.Null(), false),
			row("null_array", profile(hff.Null(), hff.Null()), false),
			row("empty_array", profile(hff.Null(), hff.List()), false),
			row("last_integer", profile(hff.Null(), hff.List(hff.Null(), hff.Int(2))), true),
			row("last_null", profile(hff.Null(), hff.List(hff.Int(2), hff.Null())), true),
		}},
	}
}
