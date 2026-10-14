package compiler

import "github.com/ornew/hff/internal/core"

// declaration is the fixed built-in contract, before receiver/argument matching.
type declaration struct {
	family, name string
	receiver     core.Type
	params       []core.Type
	result       core.Type
	min, max     int
	variadic     bool
}

func builtins(name string) []declaration {
	any := primitive(core.AnyType)
	number := union(primitive(core.IntType), primitive(core.FloatType))
	str := primitive(core.StringType)
	boolean := primitive(core.BoolType)
	integer := primitive(core.IntType)
	bytes := primitive(core.BytesType)
	timestamp := primitive(core.TimestampType)
	duration := primitive(core.DurationType)
	list := core.NewListType(any)
	object := core.NewMapType(any)
	var methods []declaration
	add := func(family string, receiver, result core.Type, params []core.Type, min, max int, variadic bool) {
		methods = append(methods, declaration{family, name, receiver, params, result, min, max, variadic})
	}
	fixed := func(family string, receiver, result core.Type, params ...core.Type) {
		add(family, receiver, result, params, len(params), len(params), false)
	}
	switch name {
	case "type", "json":
		fixed("any", any, str)
	case "exists":
		fixed("any", any, boolean)
	case "in":
		add("any", any, boolean, []core.Type{any}, 1, -1, true)
	case "and", "or":
		fixed("bool", boolean, boolean, boolean)
	case "not":
		fixed("bool", boolean, boolean)
	case "round", "ceil", "floor":
		fixed("number", number, number)
	case "len":
		fixed("string", str, integer)
		fixed("bytes", bytes, integer)
	case "contains":
		add("string", str, boolean, []core.Type{str}, 1, -1, true)
		add("list", list, boolean, []core.Type{any}, 1, -1, true)
	case "matches", "startswith", "endswith":
		fixed("string", str, boolean, str)
	case "lower", "upper":
		fixed("string", str, str)
	case "timestamp":
		fixed("string", str, timestamp)
	case "duration":
		fixed("string", str, duration)
	case "bool":
		fixed("string", str, boolean)
	case "number":
		fixed("string", str, number)
	case "fromjson":
		fixed("string", str, any)
	case "concat":
		fixed("string", str, str, str)
		fixed("list", list, list, list)
	case "frombase64", "frombase64r":
		fixed("string", str, bytes)
	case "base64", "base64r":
		fixed("bytes", bytes, str)
	case "size":
		fixed("list", list, integer)
		fixed("object", object, integer)
	case "count":
		fixed("list", list, integer)
		fixed("list", list, integer, any)
	case "filter":
		fixed("list", list, list, any)
	case "has":
		fixed("list", list, boolean, number)
		fixed("object", object, boolean, str)
	case "unique":
		fixed("list", list, boolean)
	case "sort":
		fixed("list", list, list)
	case "get":
		fixed("object", object, any, str)
	case "keys":
		fixed("object", object, core.NewListType(str))
	case "values":
		fixed("object", object, list)
	case "items":
		fixed("object", object, core.NewListType(list))
	case "map", "flatmap":
		fixed("list", list, list, primitive(core.AnyType))
	case "any", "all":
		fixed("list", list, boolean, primitive(core.AnyType))
	case "foldl", "foldr":
		fixed("list", list, any, any, primitive(core.AnyType))
	case "year", "month", "dayofmonth", "dayofyear", "milliseconds":
		add("timestamp", timestamp, integer, []core.Type{str}, 0, 1, false)
	case "hours", "minutes", "seconds":
		add("timestamp", timestamp, integer, []core.Type{str}, 0, 1, false)
	case "string":
		fixed("any", any, str)
		fixed("timestamp", timestamp, str, str)
	case "after", "before":
		fixed("timestamp", timestamp, boolean, timestamp)
	case "add":
		fixed("timestamp", timestamp, timestamp, duration)
	}
	switch name {
	case "hours", "minutes", "seconds", "milliseconds", "microseconds":
		fixed("duration", duration, primitive(core.FloatType))
	case "nanoseconds":
		fixed("duration", duration, integer)
	}
	return methods
}
func receiverMatches(actual, wanted core.Type) bool {
	if bottom(actual) || wanted.Kind() == core.AnyType {
		return true
	}
	if isObject(actual) && isObject(wanted) {
		return true
	}
	return accepted(actual, wanted)
}
