package compiler

import (
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

func specialize(d declaration, receiver core.Type, args []plan.Node, callbackResult core.Type) (core.Type, bool) {
	result := d.result
	if d.family == "any" && (d.name == "type" || d.name == "tostring" || d.name == "tojson") {
		return result, true
	}
	if bottom(receiver) {
		if receiver.Kind() == core.NeverType {
			return receiver, true
		}
		if d.family == "any" && (d.name == "type" || d.name == "tostring" || d.name == "tojson" || d.name == "exists") {
			return result, true
		}
		return primitive(core.NullType), true
	}
	switch d.family {
	case "number":
		result = receiver
	case "list":
		switch d.name {
		case "concat":
			var types []core.Type
			types = append(types, receiver.Element())
			for _, a := range args {
				types = append(types, element(a.Type()))
			}
			result = core.NewListType(union(types...))
		case "sort":
			valid := receiver.Element().Kind() == core.NeverType
			for _, t := range branches(receiver.Element()) {
				valid = valid || ordered(t)
			}
			if !valid {
				return core.Type{}, false
			}
			result = receiver
		case "map", "flatmap", "any", "all", "foldl", "foldr":
			result = callbackResult
		}
	case "object":
		switch d.name {
		case "get":
			result = objectValues(receiver)
			if len(args) > 0 {
				if value, literal := args[0].Literal(); literal {
					if key, stringKey := value.AsString(); stringKey {
						if field, known := fieldType(receiver, key); known {
							result = field
						} else {
							result = primitive(core.NullType)
						}
					}
				}
			}
		case "values":
			result = core.NewListType(objectValues(receiver))
		case "items":
			result = core.NewListType(core.NewListType(union(primitive(core.StringType), objectValues(receiver))))
		}
	}
	// Evaluation failures, incompatible union arguments, and nullable receivers
	// are checked at runtime. Method result types conservatively include null.
	if d.family == "any" && d.name == "exists" {
		return primitive(core.BoolType), true
	}
	return nullable(result), true
}
func ordered(t core.Type) bool {
	return isNumber(t) || t.Kind() == core.StringType || t.Kind() == core.TimestampType || t.Kind() == core.DurationType
}
func nonNullBranches(t core.Type) []core.Type {
	var result []core.Type
	for _, branch := range branches(t) {
		if !bottom(branch) {
			result = append(result, branch)
		}
	}
	if len(result) == 0 {
		return []core.Type{t}
	}
	return result
}
