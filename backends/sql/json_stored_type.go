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
		if typ, known := b.jsonItemSlotType(parent, index); known {
			return typ
		}
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
	if n.Kind() == hff.TypeGuardNode {
		if types, slot := b.jsonItemSlotGuardTypes(n); slot {
			target, _ := n.Constraint()
			return itemSlotGuardType(types, target)
		}
		if roles := b.jsonItemRoles(n); roles != nil {
			return guardedItemCarrier(n.Type(), roles, false)
		}
		if roles := b.jsonItemsRoles(n); roles != nil {
			return guardedItemCarrier(n.Type(), roles, true)
		}
	}
	if method, ok := n.Method(); ok {
		switch method.Name() {
		case "concat":
			if roles := b.jsonItemsRoles(n); roles != nil {
				return itemProvenanceType(jsonItemProvenance{list: roles})
			}
		case "filter", "distinct":
			// Selection preserves original elements without changing their storage
			// representation, even when the compiler widened an Any branch.
			receiver, _ := n.Child(0)
			return sortListType(b.jsonStoredType(receiver))
		case "find":
			receiver, _ := n.Child(0)
			return jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true)
		case "foldl", "foldr":
			// Successfully lowered folds have normalized JSON-native state.
			typ, _ := b.jsonFoldType(n, n.Type())
			return typ
		case "map", "flatmap":
			receiver, _ := n.Child(0)
			callback, _ := n.Child(1)
			item := jsonStoredMemberType(sortListType(b.jsonStoredType(receiver)), "", false, true)
			result := b.jsonCallbackStoredType(callback, []hff.Type{item}, b.jsonItemsRoles(receiver))
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
	case hff.BytesType, hff.DurationType, hff.TimestampType:
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
func (b *builder) jsonCallbackStoredType(callback hff.Node, types []hff.Type, roles ...*jsonItemRoles) hff.Type {
	restore := b.jsonCallbackParameters(callback, types, roles)
	defer restore()
	body, _ := callback.Child(0)
	return b.jsonStoredType(body)
}

// Temporarily recover types and roles by lexical binding ID. Captured outer
// parameters survive; same-name inner parameters never acquire their roles.
func (b *builder) jsonCallbackParameters(callback hff.Node, types []hff.Type, roles []*jsonItemRoles, lists ...[]*jsonItemRoles) func() {
	outerMemo := b.itemRoleMemo
	b.itemRoleMemo = nil
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
		value.itemRoles, value.itemListRoles = nil, nil
		if len(lists) > 0 && i < len(lists[0]) {
			value.itemListRoles = lists[0][i]
		}
		if i < len(roles) {
			value.itemRoles = roles[i]
		}
		b.parameters[p.ID()] = value
	}
	return func() {
		b.itemRoleMemo = outerMemo
		for i, p := range parameters {
			if existed[i] {
				b.parameters[p.ID()] = old[i]
			} else {
				delete(b.parameters, p.ID())
			}
		}
	}
}
