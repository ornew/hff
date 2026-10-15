package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Values inside a row plan are immutable lexical descriptors. Constructors
// retain their children instead of erasing their HFF identity in JSON. Leaves
// adapt the existing native, schema-backed JSON and item-slot carriers.
type jsonDeferredValue struct {
	typ       hff.Type
	present   string
	leaf      *jsonParameter
	list      *jsonDeferredList
	object    *[]jsonDeferredField
	choices   []jsonDeferredChoice
	selection *jsonDeferredSelection
	runtime   *jsonRuntimeValue
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
	if value.runtime != nil {
		return value.runtime.lossy && deferredTypeMayFloat(value.typ)
	}
	if value.selection != nil {
		for _, arm := range value.selection.arms {
			if deferredValueLoss(deferredLeaf(arm.value)) {
				return true
			}
		}
		return deferredValueLoss(value.selection.fallback)
	}
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
	if value, handled, err := b.deferredRuntimeObjectEnumeration(n); handled || err != nil {
		return value, handled, err
	}
	if value, handled, err := b.deferredStructValues(n); handled || err != nil {
		return value, handled, err
	}
	if value, handled, err := b.deferredDynamicStructAccess(n); handled || err != nil {
		return value, handled, err
	}
	if binding, ok := n.Binding(); ok {
		if parameter, exists := b.parameters[binding.ID()]; exists && parameter.deferred != nil {
			return parameter.deferred, true, nil
		}
	}
	if n.Kind() == hff.ListNode || n.Kind() == hff.ObjectNode {
		needed := b.keepDeferredValues || b.containsDeferredReference(n)
		if !needed {
			var err error
			needed, err = b.hasDeferredValueSource(n)
			if err != nil {
				return nil, true, err
			}
		}
		if needed {
			value, err := b.constructDeferredValue(n)
			return value, true, err
		}
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
		receiver, _ := n.Child(0)
		runtimeEnumeration := false
		switch method.Name() {
		case "sum", "avg", "min", "max", "find", "distinct", "foldl", "foldr", "map", "filter", "flatmap", "concat":
			var err error
			runtimeEnumeration, err = b.needsRuntimeObjectEnumeration(receiver)
			if err != nil {
				return nil, true, err
			}
		}
		typedSource := b.keepDeferredValues
		switch method.Name() {
		case "sum", "avg", "min", "max", "find", "sort", "distinct":
			if !typedSource {
				var err error
				typedSource, err = b.hasDeferredValueSource(n)
				if err != nil {
					return nil, true, err
				}
			}
		}
		for _, name := range []string{"sum", "avg", "min", "max"} {
			if !builtinListCall(n, name) {
				continue
			}
			receiver, _ := n.Child(0)
			if typedSource || computedDeferredSource(receiver) {
				callback, _ := n.Child(1)
				if _, _, keys := itemKeySource(receiver, callback); callback.Valid() && keys && !runtimeEnumeration {
					// Key-only aggregates do not need to recover the value slot.
					return nil, false, nil
				}
				value, err := b.deferredAggregateDescriptor(n, name)
				return value, true, err
			}
		}
		if builtinListCall(n, "find") {
			receiver, _ := n.Child(0)
			needed := typedSource || computedDeferredSource(receiver)
			if needed && (runtimeEnumeration || b.jsonItemRoles(n) == nil && b.jsonItemsRoles(n) == nil) {
				value, err := b.deferredFindDescriptor(n)
				return value, true, err
			}
		}
		if builtinListCall(n, "sort") {
			receiver, _ := n.Child(0)
			needed := typedSource || computedDeferredSource(receiver)
			if needed {
				value, err := b.runtimeSortDescriptor(n)
				return value, true, err
			}
		}
		if builtinListCall(n, "distinct") && typedSource {
			if !runtimeEnumeration && (b.jsonItemRoles(n) != nil || b.jsonItemsRoles(n) != nil) {
				return nil, false, nil
			}
			value, err := b.runtimeDistinctDescriptor(n)
			return value, true, err
		}
		if !b.runtimeFoldProbe && (builtinListCall(n, "foldl") || builtinListCall(n, "foldr")) {
			if !runtimeEnumeration && (b.jsonItemRoles(n) != nil || b.jsonItemsRoles(n) != nil) {
				// Existing positional proofs have specialized access and callback
				// lowering. A generic leaf must not erase their per-slot types.
				return nil, false, nil
			}
			value, err := b.runtimeFoldDescriptor(n, method.Name())
			return value, true, err
		}
		switch method.Name() {
		case "get", "map", "filter", "flatmap", "concat":
		default:
			return nil, false, nil
		}
		for _, name := range []string{"map", "filter", "flatmap", "concat"} {
			if builtinListCall(n, name) && (runtimeEnumeration || b.jsonItemsRoles(n) == nil) {
				needed, err := b.needsDeferredFlatmap(n)
				if err != nil {
					return nil, true, err
				}
				if !needed && !b.keepDeferredValues {
					needed, err = b.hasDeferredConstructor(n)
					if err != nil {
						return nil, true, err
					}
				}
				if !needed && !b.keepDeferredValues {
					needed, err = b.hasDeferredStructSource(n)
					if err != nil {
						return nil, true, err
					}
				}
				if needed || b.keepDeferredValues || runtimeEnumeration {
					plan, err := b.deferredList(n)
					if err != nil {
						return nil, true, err
					}
					return &jsonDeferredValue{typ: b.jsonStoredType(n), present: plan.present, list: &plan}, true, nil
				}
			}
		}
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
	if value.runtime != nil {
		return b.runtimeValueList(n, value), checkContext(b.ctx)
	}
	if value.selection != nil {
		return b.deferredSelectionList(n, value)
	}
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
	if value, handled, err := b.deferredMembership(n); handled || err != nil {
		return value, handled, err
	}
	if value, handled, err := b.deferredComparison(n); handled || err != nil {
		return value, handled, err
	}
	if method, ok := n.Method(); ok && n.ChildCount() == 1 {
		builtin := true
		for _, signature := range method.Signatures() {
			if _, custom := signature.Declaration(); custom {
				builtin = false
			}
		}
		switch method.Name() {
		case "type", "exists", "size", "count", "json", "string", "len":
			if builtin {
				receiver, _ := n.Child(0)
				observer := b
				var probe builder
				if method.Name() == "len" {
					probe = b.validationBuilder()
					observer = &probe
				}
				value, handled, err := observer.deferredObserverReceiver(receiver, method.Name())
				if handled || err != nil {
					if err != nil {
						return expression{}, handled, err
					}
					if method.Name() == "len" && (value.typ.Kind() == hff.NullType || value.typ.Kind() == hff.NeverType) {
						// A private null proof must not replace the ordinary SQL
						// carrier selected from the public result type. Probe from
						// before receiver lowering to avoid abandoned parameters.
						normal := b.validationBuilder()
						normal.keepDeferredValues = false
						result, err := normal.call(n)
						if err == nil {
							b.adoptDeferredProbe(normal)
							return result, true, nil
						}
						if err := b.typedFallbackError(err); err != nil {
							return expression{}, true, err
						}
					}
					result, err := observer.observeDeferredValue(n, value, method.Name())
					if err == nil && method.Name() == "len" {
						b.adoptDeferredProbe(probe)
					}
					return result, true, err
				}
				if method.Name() == "json" {
					return b.jsonMappedSlotSerialization(n)
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

// Stored paths already have schema-backed lowering. Computed trees
// can carry lexical types that a merged JSON list schema cannot recover.
func computedDeferredSource(n hff.Node) bool {
	if n.Kind() == hff.CallNode || n.Kind() == hff.ListNode {
		return true
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if computedDeferredSource(child) {
			return true
		}
	}
	return false
}
