package sql

import "github.com/ornew/hff"

// A null guard preserves its input value rather than converting it. In
// particular, removing null from Any must not invent unsupported storage
// alternatives after JSON-native runtime values have already been established.
func (b *builder) jsonStoredType(n hff.Node) hff.Type {
	if n.Kind() == hff.FieldNode {
		parent, _ := n.Child(0)
		key, _ := n.Field()
		return jsonStoredMemberType(b.jsonStoredType(parent), key, true, false)
	}
	if n.Kind() == hff.IndexNode {
		parent, _ := n.Child(0)
		index, _ := n.Child(1)
		literal, constant := index.Literal()
		key, stringKey := literal.AsString()
		source := jsonIndexedReceiverType(b.jsonStoredType(parent), b.jsonStoredType(index))
		return jsonStoredMemberType(source, key, constant && stringKey, true)
	}
	if n.Kind() == hff.ListNode {
		var elements []hff.Type
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			elements = append(elements, b.jsonStoredType(child))
		}
		return hff.NewListType(hff.NewUnionType(elements...))
	}
	if n.Kind() == hff.ObjectNode {
		keys, _ := n.Keys()
		var fields []hff.Field
		for i, key := range keys {
			child, _ := n.Child(i)
			fields = append(fields, hff.Field{Name: key, Type: b.jsonStoredType(child)})
		}
		return hff.NewStructType(fields...)
	}
	if n.Kind() == hff.GuardNode {
		child, _ := n.Child(0)
		return b.jsonStoredType(child)
	}
	if method, ok := n.Method(); ok {
		switch method.Name() {
		case "foldl", "foldr":
			// Successfully lowered folds have normalized JSON-native state.
			typ, _ := b.jsonFoldType(n, n.Type())
			return typ
		case "map", "flatmap":
			receiver, _ := n.Child(0)
			callback, _ := n.Child(1)
			item := jsonStoredMemberType(b.jsonStoredType(receiver), "", false, true)
			result := b.jsonCallbackStoredType(callback, []hff.Type{item})
			if method.Name() == "flatmap" {
				list := nonnullJSONType(containsListType(result))
				if list.Kind() == hff.ListType {
					result = list.Element()
				} else if list.Kind() == hff.AnyType {
					result = list
				} else {
					result = hff.PrimitiveType(hff.NeverType)
				}
			}
			return hff.NewListType(result)
		case "get", "values", "items":
			parent, _ := n.Child(0)
			key, known := "", false
			if method.Name() == "get" {
				argument, _ := n.Child(1)
				if literal, constant := argument.Literal(); constant {
					key, known = literal.AsString()
				}
			}
			typ := jsonStoredMemberType(b.jsonStoredType(parent), key, known, false)
			if method.Name() == "values" {
				return hff.NewListType(typ)
			}
			if method.Name() == "items" {
				return hff.NewListType(hff.NewListType(hff.NewUnionType(hff.PrimitiveType(hff.StringType), typ)))
			}
			return typ
		}
	}
	if binding, ok := n.Binding(); ok {
		if parameter, exists := b.parameters[binding.ID()]; exists && parameter.typ.Kind() != hff.InvalidType {
			return parameter.typ
		}
	}
	return n.Type()
}

func jsonIndexedReceiverType(typ, index hff.Type) hff.Type {
	integer, floating := jsonNumericCandidates(index)
	stringIndex := false
	var hasString func(hff.Type) bool
	hasString = func(t hff.Type) bool {
		if t.Kind() == hff.StringType || t.Kind() == hff.AnyType {
			return true
		}
		for _, member := range t.Members() {
			if hasString(member) {
				return true
			}
		}
		return false
	}
	stringIndex = hasString(index)
	switch typ.Kind() {
	case hff.UnionType:
		var members []hff.Type
		for _, member := range typ.Members() {
			members = append(members, jsonIndexedReceiverType(member, index))
		}
		return hff.NewUnionType(members...)
	case hff.AnyType:
		if integer || floating || stringIndex {
			return typ
		}
	case hff.ListType:
		if integer || floating {
			return typ
		}
	case hff.MapType, hff.StructType:
		if stringIndex {
			return typ
		}
	}
	return hff.PrimitiveType(hff.NullType)
}

// Resolve stored schemas through access without confusing a compiler guard's
// expanded type with the representation of its preserved value.
func jsonStoredMemberType(typ hff.Type, key string, known, indexed bool) hff.Type {
	null := hff.PrimitiveType(hff.NullType)
	switch typ.Kind() {
	case hff.AnyType:
		return typ
	case hff.UnionType:
		var members []hff.Type
		for _, member := range typ.Members() {
			members = append(members, jsonStoredMemberType(member, key, known, indexed))
		}
		return hff.NewUnionType(members...)
	case hff.ListType:
		if indexed && !known {
			return hff.NewUnionType(typ.Element(), null)
		}
	case hff.MapType:
		return hff.NewUnionType(typ.Element(), null)
	case hff.StructType:
		members := []hff.Type{null}
		for _, field := range typ.Fields() {
			if !known || field.Name == key {
				members = append(members, field.Type)
			}
		}
		return hff.NewUnionType(members...)
	}
	return null
}

// Seeds and callback results are validated separately. Recursive state can
// therefore narrow inferred alternatives to its established storage domain,
// including refinements nested inside constructed collections.
func jsonNativeCarrierType(typ, stored hff.Type) hff.Type {
	switch typ.Kind() {
	case hff.AnyType:
		if jsonHasTaggedString(stored) {
			return stored
		}
		return typ
	case hff.BytesType, hff.DurationType:
		if jsonHasTaggedKind(stored, typ.Kind()) {
			return typ
		}
		return hff.PrimitiveType(hff.NeverType)
	case hff.UnionType:
		var members []hff.Type
		for _, member := range typ.Members() {
			members = append(members, jsonNativeCarrierType(member, stored))
		}
		return hff.NewUnionType(members...)
	case hff.ListType:
		return hff.NewListType(jsonNativeCarrierType(typ.Element(), jsonStoredMemberType(stored, "", false, typ.Kind() == hff.ListType)))
	case hff.MapType:
		return hff.NewMapType(jsonNativeCarrierType(typ.Element(), jsonStoredMemberType(stored, "", false, typ.Kind() == hff.ListType)))
	case hff.StructType:
		fields := typ.Fields()
		for i := range fields {
			fields[i].Type = jsonNativeCarrierType(fields[i].Type, jsonStoredMemberType(stored, fields[i].Name, true, false))
		}
		return hff.NewStructType(fields...)
	default:
		if jsonNativeSchema(typ) {
			return typ
		}
		return hff.PrimitiveType(hff.NeverType)
	}
}

// Recover a callback value's storage type in its lexical parameter scope.
// No SQL is built here; existing code/provenance fields remain unchanged.
func (b *builder) jsonCallbackStoredType(callback hff.Node, types []hff.Type) hff.Type {
	parameters, _ := callback.Parameters()
	if b.parameters == nil {
		b.parameters = make(map[hff.BindingID]jsonParameter)
	}
	old := make([]jsonParameter, len(parameters))
	existed := make([]bool, len(parameters))
	for i, p := range parameters {
		old[i], existed[i] = b.parameters[p.ID()]
		value := old[i]
		value.typ = types[i]
		b.parameters[p.ID()] = value
	}
	defer func() {
		for i, p := range parameters {
			if existed[i] {
				b.parameters[p.ID()] = old[i]
			} else {
				delete(b.parameters, p.ID())
			}
		}
	}()
	body, _ := callback.Child(0)
	return b.jsonStoredType(body)
}
