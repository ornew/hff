package sql

import (
	"strings"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

// A slot observation retains its pair, selected numeric position and per-key
// schema until the observer has resolved the HFF type. This is deliberately
// local to lowering the observer: an untyped JSON string must not escape as a
// falsely identified String after selecting a Bytes/Duration/Timestamp value.
type jsonItemSlot struct {
	raw, pair, key, value, present string
	format                         JSONFormat
	roles                          *jsonItemRoles
	constraints                    []hff.Type
}

func (b *builder) jsonItemSlot(n hff.Node) (jsonItemSlot, bool, error) {
	n, guards := itemSlotOrigin(n)
	slot, handled, err := b.jsonItemSlotBase(n)
	if !handled || err != nil {
		return slot, handled, err
	}
	// A pipeline parameter can have sibling observations. Each chain owns
	// its added constraints, so one guard cannot narrow another consumer.
	slot.constraints = append([]hff.Type(nil), slot.constraints...)
	for i := len(guards) - 1; i >= 0; i-- {
		guard := guards[i]
		if guard.Kind() == hff.GuardNode {
			slot.present = both(slot.present, "(NOT "+slot.null()+")")
			continue
		}
		target, _ := guard.Constraint()
		test, err := b.jsonItemSlotTest(guard, slot, target)
		if err != nil {
			return jsonItemSlot{}, true, err
		}
		slot.present = both(slot.present, "COALESCE("+test+",FALSE)")
		if err := b.normalizeItemSlotGuard(guard, &slot, target); err != nil {
			return jsonItemSlot{}, true, err
		}
		slot.constraints = append(slot.constraints, target)
	}
	return slot, true, nil
}

// A guard does not convert an HFF value, but recovering its typed SQL
// representation can fail to ordinary null (for example an Any Float token
// outside PostgreSQL's input range). Later guards and map stages must observe
// that recovered value, not classify the original token again.
func (b *builder) normalizeItemSlotGuard(n hff.Node, slot *jsonItemSlot, target hff.Type) error {
	types := itemSlotTypes(slot.roles, slot.key != "FALSE", slot.value != "FALSE", slot.constraints)
	needed := false
	for _, source := range types {
		narrowed := intersectItemType(source, target)
		if narrowed.Kind() == hff.NeverType {
			continue
		}
		stable, err := b.normalizationPreservesJSON(source, narrowed)
		if err != nil {
			return err
		}
		needed = needed || !stable
	}
	if !needed {
		return nil
	}
	raw, err := b.jsonItemSlotOperation(n, *slot, "'null'::json", func(source hff.Type) (string, error) {
		typ := intersectItemType(source, target)
		return b.normalizedComparisonDocument(n, slot.raw, slot.format, typ)
	})
	if err != nil {
		return err
	}
	slot.raw, slot.format = b.project(raw), JSON
	return nil
}

func (b *builder) jsonItemSlotBase(n hff.Node) (jsonItemSlot, bool, error) {
	if binding, ok := n.Binding(); ok {
		if parameter, bound := b.parameters[binding.ID()]; bound && parameter.itemSlot != nil {
			return *parameter.itemSlot, true, nil
		}
	}
	if n.Kind() != hff.IndexNode {
		return jsonItemSlot{}, false, nil
	}
	// A key-only traversal has no pair carrier. Its existing scalar path
	// already knows this slot is String without observing any source values.
	if id, key := itemKeyBinding(n); key {
		if parameter, bound := b.parameters[id]; bound && parameter.itemKey != "" {
			return jsonItemSlot{}, false, nil
		}
	}
	parent, _ := n.Child(0)
	roles := b.jsonItemRoles(parent)
	if roles == nil {
		return jsonItemSlot{}, false, nil
	}
	pair, err := b.expression(parent)
	if err != nil {
		return jsonItemSlot{}, true, err
	}
	slot := jsonItemSlot{raw: "NULL::json", pair: "NULL::json", key: "FALSE", value: "FALSE", present: pair.present, format: JSON, roles: roles}
	if pair.kind != hff.NullType {
		format, ok := b.jsonValues[pair.code]
		if !ok || pair.kind != hff.ListType {
			return jsonItemSlot{}, true, b.failure(n, "object item slot requires a recoverable pair")
		}
		slot.pair, slot.format = pair.code, format
		child, _ := n.Child(1)
		index, err := b.expression(child)
		if err != nil {
			return jsonItemSlot{}, true, err
		}
		slot.present = ordinaryPresence([]expression{pair, index})
		operand, err := b.numericOperand(child, index)
		if err != nil {
			return jsonItemSlot{}, true, err
		}
		position := "NULL::integer"
		var branches []string
		if operand.integerPossible {
			branches = append(branches, "WHEN "+both(operand.isInteger, validJSONIndex(operand.integer))+" THEN "+operand.integer.code+"::integer")
		}
		if operand.floatPossible {
			branches = append(branches, "WHEN "+both(operand.isFloat, validJSONIndex(operand.floating))+" THEN "+operand.floating.code+"::integer")
		}
		if len(branches) > 0 {
			position = b.project("(CASE " + strings.Join(branches, " ") + " ELSE NULL::integer END)")
		}
		slot.key, slot.value = "("+position+" IN (0,-2))", "("+position+" IN (1,-1))"
		if x, known := itemLiteralIndex(child); known {
			slot.key, slot.value = "FALSE", "FALSE"
			if x == 0 || x == -2 {
				slot.key = "TRUE"
			}
			if x == 1 || x == -1 {
				slot.value = "TRUE"
			}
		}
		slot.raw = b.project("(" + pair.code + " -> " + position + ")")
	}
	return slot, true, nil
}

// Guards preserve the selected value. Keep the original slot available to a
// final observer instead of forcing each intermediate union into a carrier.
func itemSlotOrigin(n hff.Node) (hff.Node, []hff.Node) {
	var guards []hff.Node
	for n.Kind() == hff.GuardNode || n.Kind() == hff.TypeGuardNode {
		guards = append(guards, n)
		n, _ = n.Child(0)
	}
	return n, guards
}

func (slot jsonItemSlot) null() string {
	return "(" + slot.raw + " IS NULL OR " + string(slot.format) + "_typeof(" + slot.raw + ")='null')"
}

func (b *builder) jsonItemSlotOperation(n hff.Node, slot jsonItemSlot, nullResult string, operation func(hff.Type) (string, error)) (string, error) {
	apply := func(source hff.Type) (string, error) {
		for _, target := range slot.constraints {
			source = intersectItemType(source, target)
		}
		return operation(source)
	}
	key, value := nullResult, nullResult
	var err error
	if slot.key != "FALSE" {
		key, err = apply(itemKeyType(slot.roles))
		if err != nil {
			return "", err
		}
	}
	if slot.value != "FALSE" {
		value, err = b.jsonItemValueOperation(n, slot.pair, slot.roles, apply)
		if err != nil {
			return "", err
		}
	}
	if slot.key == "TRUE" {
		return key, nil
	}
	if slot.value == "TRUE" {
		return value, nil
	}
	return "(CASE WHEN " + slot.key + " THEN " + key + " WHEN " + slot.value + " THEN " + value + " ELSE " + nullResult + " END)", nil
}

func (b *builder) jsonItemSlotGuard(n hff.Node) (expression, bool, error) {
	child, _ := n.Child(0)
	slot, handled, err := b.jsonItemSlot(child)
	if !handled || err != nil {
		return expression{}, handled, err
	}
	target, _ := n.Constraint()
	// Validate alternatives before inference can absorb a tagged family into
	// Any. A successful guard must still have a recoverable result carrier.
	types, _ := b.jsonItemSlotGuardTypes(n)
	if err := b.checkJSONStringSchemas(n, hff.NewUnionType(types...)); err != nil {
		return expression{}, true, err
	}
	test, err := b.jsonItemSlotTest(n, slot, target)
	if err != nil {
		return expression{}, true, err
	}
	present := both(slot.present, "COALESCE("+test+",FALSE)")
	guarded := "(CASE WHEN " + present + " THEN " + slot.raw + " ELSE NULL::" + string(slot.format) + " END)"
	result, err := b.jsonDecode(n, guarded, slot.format, b.jsonStoredType(n), present)
	return result, true, err
}

func (b *builder) jsonItemSlotTest(n hff.Node, slot jsonItemSlot, target hff.Type) (string, error) {
	nullResult := "FALSE"
	if acceptsKind(target, hff.NullType) {
		nullResult = "TRUE"
	}
	return b.jsonItemSlotOperation(n, slot, nullResult, func(source hff.Type) (string, error) {
		return b.jsonMatches(n, slot.raw, slot.format, source, target)
	})
}

// Preserve the recovered alternatives before inference absorbs Any. Literal
// positions can discard the unobserved slot; dynamic positions must keep both.
func (b *builder) jsonItemSlotGuardTypes(n hff.Node) ([]hff.Type, bool) {
	target, _ := n.Constraint()
	child, _ := n.Child(0)
	child, guards := itemSlotOrigin(child)
	var roles *jsonItemRoles
	var constraints []hff.Type
	keyPossible, valuePossible := true, true
	if binding, ok := child.Binding(); ok && b.parameters[binding.ID()].itemSlot != nil {
		slot := b.parameters[binding.ID()].itemSlot
		roles, constraints = slot.roles, slot.constraints
		keyPossible, valuePossible = slot.key != "FALSE", slot.value != "FALSE"
	} else {
		if child.Kind() != hff.IndexNode {
			return nil, false
		}
		parent, _ := child.Child(0)
		roles = b.jsonItemRoles(parent)
		if roles == nil {
			return nil, false
		}
		index, _ := child.Child(1)
		x, literal := itemLiteralIndex(index)
		keyPossible = !literal || x == 0 || x == -2
		valuePossible = !literal || x == 1 || x == -1
	}
	constraints = append([]hff.Type(nil), constraints...)
	for i := len(guards) - 1; i >= 0; i-- {
		if prior, ok := guards[i].Constraint(); ok {
			constraints = append(constraints, prior)
		}
	}
	constraints = append(constraints, target)
	return itemSlotTypes(roles, keyPossible, valuePossible, constraints), true
}

func itemSlotTypes(roles *jsonItemRoles, keyPossible, valuePossible bool, constraints []hff.Type) []hff.Type {
	var types []hff.Type
	narrow := func(source hff.Type) hff.Type {
		for _, constraint := range constraints {
			source = intersectItemType(source, constraint)
		}
		return source
	}
	if keyPossible {
		types = append(types, narrow(itemKeyType(roles)))
	}
	if valuePossible {
		shape := makeComparisonShape(roles.object)
		for _, key := range shape.keys {
			types = append(types, narrow(schemaObjectValue(shape, key)))
		}
		types = append(types, narrow(schemaObjectFallback(shape)))
	}
	return types
}

// Deferred pipeline values can be observed or narrowed without decoding an
// ambiguous carrier. A general read must still recover one ordinary HFF value.
func (b *builder) jsonItemSlotValue(n hff.Node, slot jsonItemSlot) (expression, error) {
	types := itemSlotTypes(slot.roles, slot.key != "FALSE", slot.value != "FALSE", slot.constraints)
	if err := b.checkJSONStringSchemas(n, hff.NewUnionType(types...)); err != nil {
		return expression{}, err
	}
	typ := itemSlotGuardType(types, hff.PrimitiveType(hff.AnyType))
	return b.jsonDecode(n, slot.raw, slot.format, typ, slot.present)
}

func (b *builder) jsonItemSlotTruth(n hff.Node, slot jsonItemSlot) (expression, error) {
	test, err := b.jsonItemSlotOperation(n, slot, "FALSE", func(source hff.Type) (string, error) {
		if source.Kind() == hff.NeverType {
			return "FALSE", nil
		}
		value, err := b.jsonDecodeProjected(n, slot.raw, slot.format, source, "TRUE")
		if err != nil {
			return "", err
		}
		return b.truthWithType(value, source), nil
	})
	return boolean(both(slot.present, test)), err
}

func itemSlotGuardType(types []hff.Type, target hff.Type) hff.Type {
	var members []core.Type
	for _, typ := range types {
		members = append(members, core.Type(typ))
	}
	if acceptsKind(target, hff.NullType) {
		members = append(members, core.PrimitiveType(core.NullType))
	}
	result := core.UnionTypes(members...)
	if result.Kind() == core.NeverType {
		// An impossible guard has no value, but still needs a SQL carrier.
		return hff.PrimitiveType(hff.NullType)
	}
	return hff.Type(result)
}

func (b *builder) jsonItemSlotCall(n hff.Node, name string) (expression, bool, error) {
	child, _ := n.Child(0)
	slot, handled, err := b.jsonItemSlot(child)
	if !handled || err != nil {
		return expression{}, handled, err
	}
	if name == "exists" {
		return boolean(both(slot.present, "(NOT "+slot.null()+")")), true, nil
	}
	typeName, err := b.jsonItemSlotOperation(n, slot, "'null'::text", func(source hff.Type) (string, error) {
		return b.jsonRuntimeTypeName(n, slot.raw, slot.format, source)
	})
	return expression{typeName, slot.present, hff.StringType}, true, err
}
