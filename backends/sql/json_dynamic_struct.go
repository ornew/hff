package sql

import "github.com/ornew/hff"

// A single declared Struct identifies each object's keys even when disjoint
// JSON root categories are also allowed. Overlapping object alternatives and
// Any cannot acquire a discriminator from the spelling of their JSON values.
func deferredStructType(typ hff.Type) (hff.Type, bool) {
	typ = jsonObjectType(typ)
	if typ.Kind() == hff.StructType {
		return typ, true
	}
	if typ.Kind() != hff.UnionType {
		return hff.Type{}, false
	}
	var result hff.Type
	for _, member := range typ.Members() {
		if member.Kind() == hff.NullType {
			continue
		}
		if member.Kind() != hff.StructType || result.Kind() != hff.InvalidType {
			return hff.Type{}, false
		}
		result = member
	}
	return result, result.Kind() == hff.StructType
}

// Mixed roots retain separate object and list schemas. In particular, an Any
// bracket key may select either a Struct field or a numeric List position;
// object.get instead skips its key when the receiver is not an object.
func (b *builder) accessDeferredStructRoot(n hff.Node, value *jsonDeferredValue, parent expression, format JSONFormat, typ hff.Type, index *deferredAccessIndex) (*jsonDeferredValue, error) {
	if parent.kind == hff.StructType {
		return b.accessDeferredStruct(n, value, parent, format, typ, index)
	}
	operand, err := b.lowerDeferredIndex(index)
	if err != nil {
		return nil, err
	}
	class := string(format) + "_typeof(" + parent.code + ")"
	object := "(" + class + "='object')"
	objectParent := expression{code: "(CASE WHEN " + object + " THEN " + parent.code + " ELSE NULL::" + string(format) + " END)", present: parent.present, kind: hff.StructType}
	fields, err := b.accessDeferredStruct(n, value, objectParent, format, typ, index)
	if err != nil {
		return nil, err
	}
	choices := []jsonDeferredChoice{{object, fields}}
	present := parent.present
	method, call := n.Method()
	if !call || method.Name() != "get" {
		present = presenceBefore(parent, operand.present)
		list := containsListType(value.typ)
		numericKey := operand.kind == hff.AnyType || operand.kind == hff.IntType || operand.kind == hff.FloatType
		if numericKey && makeComparisonShape(list).list {
			array := "(" + class + "='array')"
			arrayParent := expression{code: "(CASE WHEN " + array + " THEN " + parent.code + " ELSE NULL::" + string(format) + " END)", present: parent.present, kind: hff.AnyType}
			stored := jsonStoredMemberType(list, "", false, true)
			selected, err := b.jsonIndexOperands(n, arrayParent, operand, format, presenceBefore(arrayParent, operand.present), stored)
			if err != nil {
				return nil, err
			}
			choices = append(choices, jsonDeferredChoice{array, deferredLeaf(jsonParameter{typ: stored, direct: &selected, format: format, lossy: value.leaf.lossy && jsonHasFloat(stored)})})
		}
	}
	return makeDeferredChoices(choices, present), nil
}

func (b *builder) deferredStructAlternatives(typ hff.Type) (bool, error) {
	fields := typ.Fields()
	seen := map[string]bool{}
	var types []hff.Type
	for _, field := range fields {
		if err := checkContext(b.ctx); err != nil {
			return false, err
		}
		if seen[field.Type.Key()] {
			continue
		}
		seen[field.Type.Key()] = true
		for _, other := range types {
			if err := checkContext(b.ctx); err != nil {
				return false, err
			}
			if jsonTaggedStringStateAmbiguous(field.Type, other) {
				return true, nil
			}
		}
		types = append(types, field.Type)
	}
	return false, nil
}

func (b *builder) dynamicStructAccessType(n hff.Node) (hff.Type, bool, error) {
	if n.Kind() != hff.IndexNode {
		method, ok := n.Method()
		if !ok || method.Name() != "get" || n.ChildCount() != 2 {
			return hff.Type{}, false, nil
		}
		for _, signature := range method.Signatures() {
			if signature.ID() != "object.get" {
				return hff.Type{}, false, nil
			}
		}
	}
	key, _ := n.Child(1)
	if _, literal := key.Literal(); literal {
		return hff.Type{}, false, nil
	}
	if !jsonRootHasKind(key.Type(), hff.StringType) && !jsonRootHasKind(key.Type(), hff.AnyType) {
		return hff.Type{}, false, nil
	}
	receiver, _ := n.Child(0)
	typ, known := deferredStructType(b.jsonStoredType(receiver))
	if !known {
		return hff.Type{}, false, nil
	}
	needed, err := b.deferredStructAlternatives(typ)
	return typ, needed, err
}

func (b *builder) deferredDynamicStructAccess(n hff.Node) (*jsonDeferredValue, bool, error) {
	_, needed, err := b.dynamicStructAccessType(n)
	if !needed || err != nil {
		return nil, err != nil, err
	}
	probe := b.validationBuilder()
	receiver, _ := n.Child(0)
	value, err := probe.declaredStructReceiver(receiver)
	if err != nil {
		return nil, true, err
	}
	result, err := probe.deferredAccess(n, value)
	if err == nil {
		b.adoptDeferredProbe(probe)
	}
	return result, true, err
}

// Keep the selector and raw extraction on the same bound field name. Even a
// later guard that discards a leaf must retain its selector parameter. Fields
// are decoded lazily; null receiver suppresses key no-value through presence.
func (b *builder) accessDeferredStruct(n hff.Node, value *jsonDeferredValue, parent expression, format JSONFormat, typ hff.Type, index *deferredAccessIndex) (*jsonDeferredValue, error) {
	operand, err := b.lowerDeferredIndex(index)
	if err != nil {
		return nil, err
	}
	present := presenceBefore(parent, operand.present)
	if operand.kind != hff.StringType && operand.kind != hff.AnyType {
		return withDeferredPresence(deferredNull(), present), nil
	}
	keyCode, stringMatch := operand.code, "TRUE"
	if operand.kind == hff.AnyType {
		raw, physical, err := b.jsonRepresentation(index.node, operand)
		if err != nil {
			return nil, err
		}
		stringMatch = b.jsonStringMatch(index.node, raw, physical)
		keyCode = "(" + raw + " #>> '{}')"
	}
	nonnull := "(" + parent.code + " IS NOT NULL AND " + string(format) + "_typeof(" + parent.code + ")<>'null')"
	var choices []jsonDeferredChoice
	for _, field := range typ.Fields() {
		if err := checkContext(b.ctx); err != nil {
			return nil, err
		}
		key, err := b.parameter(n, hff.String(field.Name))
		if err != nil {
			return nil, err
		}
		source := hff.NewUnionType(field.Type, hff.PrimitiveType(hff.NullType))
		parameter := jsonParameter{code: "(" + parent.code + " -> " + key.code + "::text)", format: format, typ: source, declared: true,
			lossy: value.leaf.lossy && jsonHasFloat(source)}
		condition := both(nonnull, both(stringMatch, "("+keyCode+" COLLATE \"C\"="+key.code+" COLLATE \"C\")"))
		choices = append(choices, jsonDeferredChoice{condition, deferredLeaf(parameter)})
	}
	return makeDeferredChoices(choices, present), nil
}

func (b *builder) hasDeferredStructSource(n hff.Node) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if _, needed, err := b.dynamicStructAccessType(n); needed || err != nil {
		return needed, err
	}
	if _, needed, err := b.structValuesType(n); needed || err != nil {
		return needed, err
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if needed, err := b.hasDeferredStructSource(child); needed || err != nil {
			return needed, err
		}
	}
	return false, nil
}
