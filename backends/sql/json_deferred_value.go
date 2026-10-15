package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Values inside a row plan are immutable lexical descriptors. Constructors
// retain their children instead of erasing their HFF identity in JSON. Leaves
// adapt the existing native, schema-backed JSON and item-slot carriers.
type jsonDeferredValue struct {
	typ     hff.Type
	present string
	leaf    *jsonParameter
	list    *jsonDeferredList
	object  *[]jsonDeferredField
	choices []jsonDeferredChoice
}

type jsonDeferredField struct {
	name  string
	value *jsonDeferredValue
}

type jsonDeferredChoice struct {
	when  string
	value *jsonDeferredValue
}

func deferredLeaf(value jsonParameter) *jsonDeferredValue {
	if value.deferred != nil {
		return value.deferred
	}
	return &jsonDeferredValue{typ: value.typ, present: deferredParameterPresence(value), leaf: &value}
}

func deferredValueParameter(value *jsonDeferredValue) jsonParameter {
	if value == nil {
		return jsonParameter{}
	}
	if value.leaf != nil {
		parameter := *value.leaf
		if parameter.itemSlot != nil {
			slot := *parameter.itemSlot
			slot.present = value.present
			parameter.itemSlot = &slot
		} else if parameter.direct != nil {
			direct := *parameter.direct
			direct.present = value.present
			parameter.direct = &direct
		} else if value.present != "TRUE" {
			return jsonParameter{typ: value.typ, deferred: value}
		}
		return parameter
	}
	return jsonParameter{typ: value.typ, deferred: value, lossy: deferredValueLoss(value)}
}

func deferredValueLoss(value *jsonDeferredValue) bool {
	if value.leaf != nil {
		return value.leaf.lossy || value.leaf.format == JSONB && deferredTypeMayFloat(value.typ)
	}
	if value.list != nil {
		for _, arm := range value.list.arms {
			if !arm.empty && deferredValueLoss(deferredLeaf(arm.value)) {
				return true
			}
		}
	}
	if value.object != nil {
		for _, field := range *value.object {
			if deferredValueLoss(field.value) {
				return true
			}
		}
	}
	for _, choice := range value.choices {
		if deferredValueLoss(choice.value) {
			return true
		}
	}
	return false
}

func deferredTypeMayFloat(typ hff.Type) bool {
	if typ.Kind() == hff.AnyType || typ.Kind() == hff.FloatType {
		return true
	}
	if typ.Kind() == hff.ListType || typ.Kind() == hff.MapType {
		return deferredTypeMayFloat(typ.Element())
	}
	for _, member := range typ.Members() {
		if deferredTypeMayFloat(member) {
			return true
		}
	}
	for _, field := range typ.Fields() {
		if deferredTypeMayFloat(field.Type) {
			return true
		}
	}
	return false
}

func deferredNull() *jsonDeferredValue {
	value := expression{code: "NULL", present: "TRUE", kind: hff.NullType}
	return deferredLeaf(jsonParameter{typ: hff.PrimitiveType(hff.NullType), direct: &value})
}

func (b *builder) constructDeferredValue(n hff.Node) (*jsonDeferredValue, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, err
	}
	if n.Kind() == hff.ListNode {
		plan, err := b.deferredList(n)
		if err != nil {
			return nil, err
		}
		return &jsonDeferredValue{typ: b.jsonStoredType(n), present: plan.present, list: &plan}, nil
	}
	fields := []jsonDeferredField{}
	keys, _ := n.Keys()
	var types []hff.Field
	present := "TRUE"
	for i, key := range keys {
		if err := b.checkTextParameter(n, key); err != nil {
			return nil, err
		}
		child, _ := n.Child(i)
		parameter, err := b.deferredValue(child)
		if err != nil {
			return nil, err
		}
		value := deferredLeaf(parameter)
		fields = append(fields, jsonDeferredField{key, value})
		types = append(types, hff.Field{Name: key, Type: value.typ})
		present = both(present, value.present)
	}
	return &jsonDeferredValue{typ: hff.NewStructType(types...), present: present, object: &fields}, nil
}

// Descriptor lookup does not cache AST nodes across lexical scopes. A returned
// row plan is rebound to the current environment before its rows are consumed.
func (b *builder) deferredDescriptor(n hff.Node) (*jsonDeferredValue, bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, false, err
	}
	if binding, ok := n.Binding(); ok {
		if parameter, exists := b.parameters[binding.ID()]; exists && parameter.deferred != nil {
			return parameter.deferred, true, nil
		}
	}
	if (n.Kind() == hff.ListNode || n.Kind() == hff.ObjectNode) && b.containsDeferredReference(n) {
		value, err := b.constructDeferredValue(n)
		return value, true, err
	}
	switch n.Kind() {
	case hff.FieldNode, hff.IndexNode:
		parent, _ := n.Child(0)
		value, handled, err := b.deferredDescriptor(parent)
		if !handled || err != nil {
			return nil, handled, err
		}
		result, err := b.deferredAccess(n, value)
		return result, true, err
	case hff.GuardNode, hff.TypeGuardNode:
		child, _ := n.Child(0)
		value, handled, err := b.deferredDescriptor(child)
		if !handled || err != nil {
			return nil, handled, err
		}
		result, err := b.guardDeferredValue(n, value)
		return result, true, err
	}
	if method, ok := n.Method(); ok {
		switch method.Name() {
		case "get", "map", "filter", "flatmap", "concat":
		default:
			return nil, false, nil
		}
		for _, name := range []string{"map", "filter", "flatmap", "concat"} {
			if builtinListCall(n, name) && b.jsonItemsRoles(n) == nil {
				needed, err := b.needsDeferredFlatmap(n)
				if err != nil {
					return nil, true, err
				}
				if needed {
					plan, err := b.deferredList(n)
					if err != nil {
						return nil, true, err
					}
					return &jsonDeferredValue{typ: b.jsonStoredType(n), present: plan.present, list: &plan}, true, nil
				}
			}
		}
		receiver, _ := n.Child(0)
		value, handled, err := b.deferredDescriptor(receiver)
		if !handled || err != nil {
			return nil, handled, err
		}
		if method.Name() == "get" {
			result, err := b.deferredAccess(n, value)
			return result, true, err
		}
		for _, name := range []string{"map", "filter", "flatmap", "concat"} {
			if builtinListCall(n, name) {
				plan, err := b.deferredList(n)
				if err != nil {
					return nil, true, err
				}
				return &jsonDeferredValue{typ: b.jsonStoredType(n), present: plan.present, list: &plan}, true, nil
			}
		}
	}
	return nil, false, nil
}

func (b *builder) containsDeferredReference(n hff.Node) bool {
	if binding, ok := n.Binding(); ok {
		parameter := b.parameters[binding.ID()]
		if parameter.deferred != nil || parameter.itemSlot != nil {
			return true
		}
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if b.containsDeferredReference(child) {
			return true
		}
	}
	return false
}

func (b *builder) deferredValueList(n hff.Node, value *jsonDeferredValue) (jsonDeferredList, error) {
	if value.list == nil {
		if value.choices != nil {
			plan := jsonDeferredList{null: "TRUE", present: value.present}
			var nulls, presence []string
			for _, choice := range value.choices {
				child, err := b.deferredValueList(n, choice.value)
				if err != nil {
					return jsonDeferredList{}, err
				}
				nulls, presence = append(nulls, child.null), append(presence, child.present)
				for _, arm := range child.arms {
					arm.selected = both(choice.when, arm.selected)
					plan.arms = append(plan.arms, arm)
				}
			}
			plan.null = deferredCases(value.choices, nulls, "TRUE")
			plan.present = both(value.present, deferredCases(value.choices, presence, "TRUE"))
			return plan, nil
		}
		if value.leaf != nil {
			if value.leaf.itemSlot != nil {
				slot := *value.leaf.itemSlot
				slot.present = value.present
				return b.deferredSlotList(n, slot)
			}
			carrier, err := b.deferredLeafExpression(n, *value.leaf)
			if err != nil {
				return jsonDeferredList{}, err
			}
			if carrier.kind != hff.ListType && carrier.kind != hff.AnyType {
				return jsonDeferredList{null: "TRUE", present: value.present}, nil
			}
			carrier, format, err := b.jsonCallbackReceiver(n, carrier)
			if err != nil {
				return jsonDeferredList{}, err
			}
			alias := b.nextAlias()
			return jsonDeferredList{null: "(" + carrier.code + " IS NULL)", present: value.present, arms: []jsonDeferredArm{{
				from: string(format) + "_array_elements(" + carrier.code + ") WITH ORDINALITY AS " + alias + "(value,ord)", ordinal: []string{alias + ".ord"}, selected: "TRUE",
				value: jsonParameter{code: alias + ".value", format: format, typ: jsonStoredMemberType(sortListType(value.typ), "", false, true), lossy: value.leaf.lossy}, parameters: copyJSONParameters(b.parameters),
			}}}, nil
		}
		return jsonDeferredList{null: "TRUE", present: value.present}, nil
	}
	plan := *value.list
	plan.present = value.present
	plan.arms = append([]jsonDeferredArm(nil), plan.arms...)
	for i := range plan.arms {
		if err := checkContext(b.ctx); err != nil {
			return jsonDeferredList{}, err
		}
		arm := &plan.arms[i]
		arm.ordinal = append([]string(nil), arm.ordinal...)
		parameters := copyJSONParameters(b.parameters)
		for id, parameter := range arm.parameters {
			parameters[id] = parameter
		}
		arm.parameters = parameters
	}
	return plan, nil
}

func (b *builder) deferredLeafExpression(n hff.Node, parameter jsonParameter) (expression, error) {
	if parameter.itemSlot != nil {
		return b.jsonItemSlotValue(n, *parameter.itemSlot)
	}
	if parameter.direct != nil {
		return *parameter.direct, nil
	}
	return b.jsonDecode(n, parameter.code, parameter.format, parameter.typ, "TRUE")
}

func (b *builder) deferredExpression(n hff.Node) (expression, bool, error) {
	if method, ok := n.Method(); ok && n.ChildCount() == 1 {
		builtin := true
		for _, signature := range method.Signatures() {
			if _, custom := signature.Declaration(); custom {
				builtin = false
			}
		}
		switch method.Name() {
		case "type", "exists", "size", "count", "json", "string":
			if builtin {
				receiver, _ := n.Child(0)
				value, handled, err := b.deferredDescriptor(receiver)
				if handled || err != nil {
					if err != nil {
						return expression{}, handled, err
					}
					result, err := b.observeDeferredValue(n, value, method.Name())
					return result, true, err
				}
			}
		}
	}
	if n.Kind() == hff.BoolNode || n.Kind() == hff.NotNode {
		child, _ := n.Child(0)
		value, handled, err := b.deferredDescriptor(child)
		if handled || err != nil {
			if err != nil {
				return expression{}, handled, err
			}
			truth, err := b.observeDeferredValue(n, value, "truth")
			if n.Kind() == hff.NotNode {
				truth.code = "(NOT " + truth.code + ")"
			}
			return truth, true, err
		}
	}
	value, handled, err := b.deferredDescriptor(n)
	if !handled || err != nil {
		return expression{}, handled, err
	}
	result, err := b.materializeDeferredValue(n, value)
	return result, true, err
}

func deferredChoiceType(choices []jsonDeferredChoice) hff.Type {
	types := []hff.Type{hff.PrimitiveType(hff.NullType)}
	for _, choice := range choices {
		types = append(types, choice.value.typ)
	}
	return hff.NewUnionType(types...)
}

func deferredCases(choices []jsonDeferredChoice, values []string, fallback string) string {
	var branches []string
	for i, choice := range choices {
		branches = append(branches, "WHEN "+choice.when+" THEN "+values[i])
	}
	if len(branches) == 0 {
		return fallback
	}
	return "(CASE " + strings.Join(branches, " ") + " ELSE " + fallback + " END)"
}
