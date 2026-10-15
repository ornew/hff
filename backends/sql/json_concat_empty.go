package sql

import "github.com/ornew/hff"

// This proves only zero list rows. Evaluation can still produce null or
// no-value; callers must preserve the evaluated carrier and its presence.
func (b *builder) staticallyEmptyJSONList(n hff.Node) (bool, error) {
	if err := checkContext(b.ctx); err != nil {
		return false, err
	}
	if n.Kind() == hff.ListNode && n.ChildCount() == 0 {
		return true, nil
	}
	if emptyListSchema(b.jsonStoredType(n)) {
		return true, nil
	}
	if _, enumeration := objectEnumeration(n); enumeration {
		receiver, _ := n.Child(0)
		if receiver.Kind() == hff.ObjectNode && receiver.ChildCount() == 0 || emptyMapSchema(b.jsonStoredType(receiver)) {
			return true, nil
		}
	}
	if builtinListCall(n, "filter") {
		callback, _ := n.Child(1)
		body, _ := callback.Child(0)
		for body.Kind() == hff.BoolNode {
			body, _ = body.Child(0)
		}
		if literal, ok := body.Literal(); ok {
			if truth, boolean := literal.AsBool(); boolean && !truth {
				return true, nil
			}
		}
	}
	for _, name := range []string{"map", "filter", "flatmap", "sort", "distinct"} {
		if builtinListCall(n, name) {
			receiver, _ := n.Child(0)
			return b.staticallyEmptyJSONList(receiver)
		}
	}
	if listConcatNode(n) {
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		empty, err := b.staticallyEmptyJSONList(left)
		if !empty || err != nil {
			return empty, err
		}
		return b.staticallyEmptyJSONList(right)
	}
	return false, nil
}

func emptyMapSchema(typ hff.Type) bool {
	if typ.Kind() == hff.MapType {
		return typ.Element().Kind() == hff.NeverType
	}
	if typ.Kind() != hff.UnionType {
		return false
	}
	object := false
	for _, member := range typ.Members() {
		if member.Kind() == hff.NullType || member.Kind() == hff.NeverType {
			continue
		}
		if !emptyMapSchema(member) {
			return false
		}
		object = true
	}
	return object
}

func emptyListSchema(typ hff.Type) bool {
	if typ.Kind() == hff.ListType {
		return typ.Element().Kind() == hff.NeverType
	}
	if typ.Kind() != hff.UnionType {
		return false
	}
	list := false
	for _, member := range typ.Members() {
		if member.Kind() == hff.NullType || member.Kind() == hff.NeverType {
			continue
		}
		if !emptyListSchema(member) {
			return false
		}
		list = true
	}
	return list
}

// Build validation remains ordered even when normalization emits no rows.
// Discard generated SQL and parameters rather than leaving orphan bindings.
func (b *builder) concatOperandDocument(n hff.Node, raw string, format JSONFormat, typ hff.Type) (string, error) {
	empty, err := b.staticallyEmptyJSONList(n)
	if err != nil {
		return "", err
	}
	if !empty {
		return b.comparisonDocument(n, raw, format, typ)
	}
	validation := b.validationBuilder()
	if _, err := validation.comparisonDocument(n, raw, format, typ); err != nil {
		return "", err
	}
	return "(CASE WHEN " + raw + " IS NULL OR " + string(format) + "_typeof(" + raw + ")='null' THEN 'null'::json ELSE '[]'::json END)", nil
}
