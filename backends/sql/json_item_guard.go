package sql

import (
	"strings"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
)

func intersectItemType(actual, target hff.Type) hff.Type {
	return hff.Type(core.IntersectTypes(core.Type(actual), core.Type(target)))
}

func itemKeyType(roles *jsonItemRoles) hff.Type {
	if roles.key.Kind() == hff.InvalidType {
		return hff.PrimitiveType(hff.StringType)
	}
	return roles.key
}

// A whole-list guard narrows each observed slot but does not erase the
// original object schema needed to recover a value's per-key identity.
func narrowItemRoles(roles *jsonItemRoles, target hff.Type) *jsonItemRoles {
	if roles == nil {
		return nil
	}
	element := itemPairGuardElement(target)
	return &jsonItemRoles{object: narrowItemSelector(roles.object, element), key: intersectItemType(itemKeyType(roles), element), value: intersectItemType(roles.value, element)}
}

func itemPairGuardElement(target hff.Type) hff.Type {
	if target.Kind() == hff.ListType && !acceptsKind(target.Element(), hff.StringType) {
		return hff.PrimitiveType(hff.NeverType)
	}
	if target.Kind() == hff.UnionType {
		var elements []core.Type
		for _, member := range target.Members() {
			elements = append(elements, core.Type(itemPairGuardElement(member)))
		}
		return hff.Type(core.UnionTypes(elements...))
	}
	return itemGuardElement(target)
}

// This is a selector schema for one retained key/value entry, not a claim
// that the entire source object passed the pair's guard. Other required
// fields may be absent from this selection; keep their viable value types.
func narrowItemSelector(source, element hff.Type) hff.Type {
	switch source.Kind() {
	case hff.AnyType:
		return hff.NewMapType(element)
	case hff.MapType:
		return hff.NewMapType(intersectItemType(source.Element(), element))
	case hff.StructType:
		fields := source.Fields()
		for i := range fields {
			fields[i].Optional = true
			fields[i].Type = intersectItemType(fields[i].Type, element)
		}
		return hff.NewStructType(fields...)
	case hff.UnionType:
		var types []core.Type
		for _, member := range source.Members() {
			types = append(types, core.Type(narrowItemSelector(member, element)))
		}
		return hff.Type(core.UnionTypes(types...))
	}
	return source
}

func itemGuardElement(target hff.Type) hff.Type {
	switch target.Kind() {
	case hff.AnyType:
		return target
	case hff.ListType:
		return target.Element()
	case hff.UnionType:
		var elements []core.Type
		for _, member := range target.Members() {
			elements = append(elements, core.Type(itemGuardElement(member)))
		}
		return hff.Type(core.UnionTypes(elements...))
	}
	return hff.PrimitiveType(hff.NeverType)
}

// Keep the positional representation after matching. A homogeneous inferred
// union such as list[string]|list[bytes] does not describe the retained pairs:
// their String keys have already ruled out the byte-only alternative.
func guardedItemCarrier(typ hff.Type, roles *jsonItemRoles, list bool) hff.Type {
	null := hff.PrimitiveType(hff.NullType)
	if sortListType(typ).Kind() != hff.ListType {
		return null
	}
	pair := hff.PrimitiveType(hff.NeverType)
	if itemKeyType(roles).Kind() != hff.NeverType && roles.value.Kind() != hff.NeverType {
		pair = itemPairType(roles)
	}
	if list {
		if acceptsKind(itemGuardElement(typ), hff.NullType) {
			pair = hff.Type(core.UnionTypes(core.Type(pair), core.Type(null)))
		}
		pair = hff.NewListType(pair)
	} else if pair.Kind() == hff.NeverType {
		return null
	}
	if acceptsKind(typ, hff.NullType) {
		return hff.Type(core.UnionTypes(core.Type(pair), core.Type(null)))
	}
	return pair
}

func (b *builder) jsonItemGuard(n hff.Node, code string, format JSONFormat, receiver hff.Node, target hff.Type) (string, bool, error) {
	if roles := b.jsonItemRoles(receiver); roles != nil {
		test, err := b.jsonItemMatches(n, code, format, roles, target, false)
		return test, true, err
	}
	if roles := b.jsonItemsRoles(receiver); roles != nil {
		test, err := b.jsonItemMatches(n, code, format, roles, target, true)
		return test, true, err
	}
	return "", false, nil
}

func (b *builder) jsonItemMatches(n hff.Node, code string, format JSONFormat, roles *jsonItemRoles, target hff.Type, list bool) (string, error) {
	if err := checkContext(b.ctx); err != nil {
		return "", err
	}
	null := "(" + code + " IS NULL OR " + string(format) + "_typeof(" + code + ")='null')"
	switch target.Kind() {
	case hff.AnyType:
		return "TRUE", nil
	case hff.NullType:
		return null, nil
	case hff.UnionType:
		var parts []string
		for _, member := range target.Members() {
			test, err := b.jsonItemMatches(n, code, format, roles, member, list)
			if err != nil {
				return "", err
			}
			parts = append(parts, test)
		}
		return "(" + strings.Join(parts, " OR ") + ")", nil
	case hff.ListType:
		if list {
			alias := b.nextAlias()
			test, err := b.jsonItemMatches(n, alias+".value", format, roles, target.Element(), false)
			if err != nil {
				return "", err
			}
			safe := "(CASE WHEN NOT " + null + " THEN " + code + " ELSE '[]'::" + string(format) + " END)"
			return both("(NOT "+null+")", "NOT EXISTS(SELECT 1 FROM "+string(format)+"_array_elements("+safe+") AS "+alias+"(value) WHERE NOT COALESCE("+test+",FALSE))"), nil
		}
		// Every non-null proven pair has a String key. A byte-only (or
		// otherwise disjoint) element constraint cannot pass, regardless
		// of whether the unused value admits dynamic tagged dispatch.
		if itemKeyType(roles).Kind() == hff.NeverType || !acceptsKind(target.Element(), hff.StringType) {
			return "FALSE", nil
		}
		value, err := b.jsonItemValueMatches(n, code, format, roles, target.Element())
		return both("(NOT "+null+")", value), err
	default:
		return "FALSE", nil
	}
}

func (b *builder) jsonItemValueMatches(n hff.Node, code string, format JSONFormat, roles *jsonItemRoles, target hff.Type) (string, error) {
	value := "(" + code + " -> 1)"
	return b.jsonItemValueOperation(n, code, roles, func(source hff.Type) (string, error) {
		return b.jsonMatches(n, value, format, source, target)
	})
}

// Each key keeps its own value contract even when the homogeneous pair type
// merges several JSON string families. Never recover a tag from its spelling.
func (b *builder) jsonItemValueOperation(n hff.Node, code string, roles *jsonItemRoles, operation func(hff.Type) (string, error)) (string, error) {
	shape := makeComparisonShape(roles.object)
	// The selector already includes each whole-pair guard. The homogeneous
	// value summary joins other keys' types, so intersecting it back into a
	// field would invent tagged alternatives for an unrelated Any field.
	fallback, err := operation(schemaObjectFallback(shape))
	if err != nil {
		return "", err
	}
	if len(shape.keys) == 0 {
		return fallback, nil
	}
	parts := []string{"CASE"}
	for _, field := range shape.keys {
		test, err := operation(schemaObjectValue(shape, field))
		if err != nil {
			return "", err
		}
		key, err := b.parameter(n, hff.String(field))
		if err != nil {
			return "", err
		}
		parts = append(parts, "WHEN ("+code+" ->> 0) COLLATE \"C\"="+key.code+" COLLATE \"C\" THEN "+test)
	}
	return "(" + strings.Join(parts, " ") + " ELSE " + fallback + " END)", nil
}
