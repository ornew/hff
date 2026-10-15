package sql

import "github.com/ornew/hff"

// A filter changes elements, but never a List receiver's outer null/presence.
// Validate discarded callbacks separately before a shape-only observation so
// unused payload parameters and row work do not escape into the real query.
func (b *builder) deferredObserverReceiver(n hff.Node, name string) (*jsonDeferredValue, bool, error) {
	if name == "type" || name == "exists" || name == "size" || name == "count" {
		if value, handled, err := b.deferredNumericGuardRoot(n); handled || err != nil {
			return value, handled, err
		}
	}
	if name == "len" {
		return b.typedValueDescriptor(n)
	}
	if (name != "type" && name != "exists") || !deferredShapeFilter(n) {
		return b.deferredDescriptor(n)
	}
	probe := b.validationBuilder()
	_, handled, err := probe.deferredDescriptor(n)
	if err != nil || !handled {
		return nil, handled, err
	}
	value, err := b.deferredOuterShape(n)
	return value, true, err
}

// Numeric recovery may replace child values with null, but cannot change a
// matched container's outer category or cardinality. This descriptor is used
// only by the immediate root observer; it must not escape to value consumers.
func (b *builder) deferredNumericGuardRoot(n hff.Node) (*jsonDeferredValue, bool, error) {
	if n.Kind() != hff.TypeGuardNode {
		return nil, false, nil
	}
	target, _ := n.Constraint()
	if (target.Kind() != hff.ListType && target.Kind() != hff.MapType) || !guardTargetRecoversNumbers(target) {
		return nil, false, nil
	}
	// Keep normal lowering as the validation authority, including callbacks,
	// storage ambiguity and metadata checks. Its SQL and arguments are not
	// adopted, so payload reconstruction cannot leak into the actual query.
	validation := b.validationBuilder()
	if _, err := validation.expression(n); err != nil {
		if fatal := b.typedFallbackError(err); fatal != nil {
			return nil, true, fatal
		}
		return nil, false, nil
	}
	probe := b.validationBuilder()
	receiver, _ := n.Child(0)
	value, err := probe.expression(receiver)
	if err != nil {
		if fatal := b.typedFallbackError(err); fatal != nil {
			return nil, true, fatal
		}
		return nil, false, nil
	}
	// A declared identical guard already avoids element validation. Preserve
	// that stronger input-contract proof instead of adding a fresh scan.
	if receiver.Type().Equal(target) && (probe.nonnullScalarInput(receiver, value) || probe.identicalInputGuard(receiver)) {
		return nil, false, nil
	}
	format := probe.jsonValues[value.code]
	if format == "" {
		return nil, false, nil
	}
	result, err := probe.jsonTypeGuardResult(n, value, format, receiver.Type(), target, false)
	if err != nil {
		return nil, true, err
	}
	typ := probe.jsonStoredType(n)
	parameter := jsonParameter{typ: typ, direct: &result, lossy: probe.losesFloat(n, nil)}
	b.adoptDeferredProbe(probe)
	return deferredLeaf(parameter), true, nil
}

// Only filter and concat have the outer-state identities used here. In
// particular, a map callback may introduce no-value through a captured value.
func deferredShapeFilter(n hff.Node) bool {
	if builtinListCall(n, "filter") {
		return true
	}
	if builtinListCall(n, "concat") {
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		return deferredShapeFilter(left) || deferredShapeFilter(right)
	}
	return false
}

func (b *builder) deferredOuterShape(n hff.Node) (*jsonDeferredValue, error) {
	if err := checkContext(b.ctx); err != nil {
		return nil, err
	}
	if builtinListCall(n, "concat") {
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		l, err := b.deferredOuterShape(left)
		if err != nil {
			return nil, err
		}
		r, err := b.deferredOuterShape(right)
		if err != nil {
			return nil, err
		}
		lc, err := b.deferredListCategory(left, l)
		if err != nil {
			return nil, err
		}
		rc, err := b.deferredListCategory(right, r)
		if err != nil {
			return nil, err
		}
		plan := &jsonDeferredList{null: "(NOT (" + lc + " AND " + rc + "))"}
		plan.present = both(l.present, "((NOT "+lc+") OR "+r.present+")")
		return &jsonDeferredValue{typ: n.Type(), present: plan.present, list: plan}, nil
	}
	if builtinListCall(n, "filter") {
		receiver, _ := n.Child(0)
		value, err := b.deferredOuterShape(receiver)
		if err != nil {
			return nil, err
		}
		if deferredOnlyListOrNull(value.typ) {
			return value, nil
		}
		// Non-list runtime branches are inapplicable to filter and produce
		// null. Retain the descriptor behind a proven List discriminator.
		matches, err := b.deferredListCategory(n, value)
		if err != nil {
			return nil, err
		}
		return makeDeferredChoices([]jsonDeferredChoice{{matches, value}}, value.present), nil
	}
	if value, handled, err := b.deferredDescriptor(n); handled || err != nil {
		return value, err
	}
	plan, err := b.deferredList(n)
	if err != nil {
		return nil, err
	}
	return &jsonDeferredValue{typ: n.Type(), present: plan.present, list: &plan}, nil
}

func deferredOnlyListOrNull(typ hff.Type) bool {
	if typ.Kind() == hff.UnionType {
		for _, member := range typ.Members() {
			if !deferredOnlyListOrNull(member) {
				return false
			}
		}
		return true
	}
	return typ.Kind() == hff.ListType || typ.Kind() == hff.NullType || typ.Kind() == hff.NeverType
}

// Testing only the outer category must not inspect or normalize elements.
func (b *builder) deferredListCategory(n hff.Node, value *jsonDeferredValue) (string, error) {
	test, err := b.deferredListCategoryTest(n, value)
	if err != nil {
		return "", err
	}
	// A missing/null JSON carrier has no category. Keep this discriminator
	// two-valued before using it for concat's receiver-null short circuit.
	return "COALESCE(" + test + ",FALSE)", nil
}

func (b *builder) deferredListCategoryTest(n hff.Node, value *jsonDeferredValue) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	if value.runtime != nil {
		return "(" + value.runtime.code + "->>0='list')", nil
	}
	if value.selection != nil {
		result, err := b.resolveDeferredSelection(n, value, hff.BoolType, func(child *jsonDeferredValue) (expression, error) {
			test, err := b.deferredListCategory(n, child)
			return expression{test, child.present, hff.BoolType}, err
		})
		return result.code, err
	}
	if value.choices != nil {
		tests := make([]string, len(value.choices))
		for i, choice := range value.choices {
			var err error
			tests[i], err = b.deferredListCategory(n, choice.value)
			if err != nil {
				return "", err
			}
		}
		return deferredCases(value.choices, tests, "FALSE"), nil
	}
	if value.list != nil {
		return "(NOT " + value.list.null + ")", nil
	}
	if value.leaf != nil {
		parameter := value.leaf
		if parameter.itemSlot != nil {
			return "(" + string(parameter.itemSlot.format) + "_typeof(" + parameter.itemSlot.raw + ")='array')", nil
		}
		if parameter.direct != nil {
			if format := b.jsonValues[parameter.direct.code]; format != "" {
				return "(" + string(format) + "_typeof(" + parameter.direct.code + ")='array')", nil
			}
		} else if parameter.format != "" {
			return "(" + string(parameter.format) + "_typeof(" + parameter.code + ")='array')", nil
		}
	}
	return "FALSE", nil
}
