package sql

import (
	"strings"

	"github.com/ornew/hff"
)

// Dispatch only signatures admitted by compilation. A wrong receiver family
// returns ordinary null before its arguments can propagate no-value. Strings
// propagate null arguments; lists treat them as values to search for instead.
func (b *builder) dynamicJSONContains(n hff.Node, values []expression) (expression, error) {
	receiver := values[0]
	if receiver.kind == hff.StringType {
		return b.dynamicJSONStringCall(n, "contains", values)
	}
	if receiver.kind == hff.ListType {
		return b.jsonMembership(n, "contains", values, b.jsonValues[receiver.code])
	}
	if receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	format, ok := b.jsonValues[receiver.code]
	if !ok || receiver.kind != hff.AnyType {
		return expression{}, b.failure(n, "dynamic contains requires a recoverable JSON receiver")
	}
	method, _ := n.Method()
	var stringAllowed, listAllowed bool
	for _, signature := range method.Signatures() {
		switch signature.ID() {
		case "string.contains":
			stringAllowed = true
		case "list.contains":
			listAllowed = true
		}
	}
	class := string(format) + "_typeof(" + receiver.code + ")"
	code := "CASE WHEN " + receiver.code + " IS NULL THEN NULL"
	present := "CASE WHEN " + receiver.code + " IS NULL THEN " + receiver.present
	if stringAllowed {
		value, err := b.dynamicJSONStringCall(n, "contains", values)
		if err != nil {
			return expression{}, err
		}
		code += " WHEN " + class + "='string' THEN " + value.code
		present += " WHEN " + class + "='string' THEN " + value.present
	}
	if listAllowed {
		r, _ := n.Child(0)
		listType := containsListType(r.Type())
		// Normalize the entire receiver using the declared schema, then select
		// its array members. This preserves numeric kinds inside union lists.
		var needles []hff.Type
		for i := 1; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			needles = append(needles, child.Type())
		}
		document, err := b.comparisonDocumentAgainst(n, receiver.code, format, listType, hff.NewUnionType(needles...))
		if err != nil {
			return expression{}, err
		}
		document = b.project(document)
		var matches []string
		for i, arg := range values[1:] {
			child, _ := n.Child(i + 1)
			raw, argFormat, err := b.jsonRepresentation(child, arg)
			if err != nil {
				return expression{}, err
			}
			alias := b.nextAlias()
			equal, err := b.jsonEqual(n, alias+".value", JSON, containsElementType(listType), raw, argFormat, child.Type())
			if err != nil {
				return expression{}, err
			}
			matches = append(matches, "EXISTS(SELECT 1 FROM json_array_elements(CASE WHEN json_typeof("+document+")='array' THEN "+document+" ELSE '[]'::json END) AS "+alias+"(value) WHERE "+equal+")")
		}
		code += " WHEN " + class + "='array' THEN (" + strings.Join(matches, " AND ") + ")"
		present += " WHEN " + class + "='array' THEN " + membershipPresence(values)
	}
	return expression{"(" + code + " ELSE NULL::boolean END)", "(" + present + " ELSE " + receiver.present + " END)", hff.BoolType}, nil
}

func containsElementType(typ hff.Type) hff.Type {
	if typ.Kind() == hff.AnyType {
		return typ
	}
	if typ.Kind() == hff.ListType {
		return typ.Element()
	}
	var elements []hff.Type
	for _, member := range typ.Members() {
		elements = append(elements, member.Element())
	}
	return hff.NewUnionType(elements...)
}

// Non-list receiver alternatives return null without membership evaluation.
// Their numeric ambiguity must not impose metadata requirements on list data.
func containsListType(typ hff.Type) hff.Type {
	if typ.Kind() != hff.UnionType {
		return typ
	}
	var lists []hff.Type
	for _, member := range typ.Members() {
		if member.Kind() == hff.AnyType {
			return member
		}
		if member.Kind() == hff.ListType {
			lists = append(lists, member)
		}
	}
	return hff.NewUnionType(lists...)
}
