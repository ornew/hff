package compiler

import "github.com/ornew/hff/internal/core"

func primitive(k core.TypeKind) core.Type { return core.PrimitiveType(k) }
func union(types ...core.Type) core.Type {
	var values []core.Type
	for _, t := range types {
		if t.Kind() == core.AnyType {
			return t
		}
		if t.Kind() == core.UnionType {
			values = append(values, t.Members()...)
		} else if t.Kind() != core.NeverType {
			values = append(values, t)
		}
	}
	filtered := values[:0]
	for _, t := range values {
		if t.Kind() == core.AnyType {
			return t
		}
		if t.Kind() != core.NeverType {
			filtered = append(filtered, t)
		}
	}
	return core.NewUnionType(filtered...)
}
func nullable(t core.Type) core.Type { return union(t, primitive(core.NullType)) }
func branches(t core.Type) []core.Type {
	if t.Kind() == core.UnionType {
		return t.Members()
	}
	if t.Kind() == core.AnyType {
		return []core.Type{primitive(core.NullType), primitive(core.BoolType), primitive(core.IntType), primitive(core.FloatType), primitive(core.StringType), primitive(core.BytesType), core.NewListType(primitive(core.AnyType)), core.NewMapType(primitive(core.AnyType)), primitive(core.TimestampType), primitive(core.DurationType)}
	}
	return []core.Type{t}
}
func isNumber(t core.Type) bool { return t.Kind() == core.IntType || t.Kind() == core.FloatType }
func isObject(t core.Type) bool { return t.Kind() == core.StructType || t.Kind() == core.MapType }
func bottom(t core.Type) bool   { return t.Kind() == core.NullType || t.Kind() == core.NeverType }
func withoutNull(t core.Type) core.Type {
	var types []core.Type
	for _, branch := range branches(t) {
		if branch.Kind() != core.NullType {
			types = append(types, branch)
		}
	}
	return union(types...)
}
func element(t core.Type) core.Type {
	var types []core.Type
	for _, branch := range branches(t) {
		if branch.Kind() == core.ListType {
			types = append(types, branch.Element())
		}
	}
	return union(types...)
}
func fieldType(t core.Type, name string) (core.Type, bool) {
	var types []core.Type
	applicable := false
	for _, branch := range branches(t) {
		switch branch.Kind() {
		case core.AnyType:
			types = append(types, branch)
			applicable = true
		case core.NullType, core.NeverType:
			types = append(types, branch)
		case core.MapType:
			types = append(types, nullable(branch.Element()))
			applicable = true
		case core.StructType:
			found := false
			for _, f := range branch.Fields() {
				if f.Name == name {
					typ := f.Type
					if f.Optional {
						typ = nullable(typ)
					}
					types = append(types, typ)
					found = true
					applicable = true
					break
				}
			}
			if !found {
				types = append(types, primitive(core.NullType))
			}
		default:
			types = append(types, primitive(core.NullType))
		}
	}
	if bottom(t) {
		applicable = true
	}
	return union(types...), applicable
}
func objectValues(t core.Type) core.Type {
	if t.Kind() == core.MapType {
		return t.Element()
	}
	var types []core.Type
	for _, f := range t.Fields() {
		types = append(types, f.Type)
	}
	return union(types...)
}
func accepted(actual, wanted core.Type) bool {
	if actual.Kind() == core.NeverType || actual.Kind() == core.NullType || wanted.Kind() == core.AnyType {
		return true
	}
	for _, a := range branches(actual) {
		for _, w := range branches(wanted) {
			if bottom(a) || a.Kind() == w.Kind() || (isNumber(a) && isNumber(w)) {
				return true
			}
		}
	}
	return false
}
