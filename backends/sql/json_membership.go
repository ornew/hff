package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) jsonRepresentation(n hff.Node, value expression) (string, JSONFormat, error) {
	if format, ok := b.jsonValues[value.code]; ok {
		return value.code, format, nil
	}
	if value.kind == hff.NullType {
		return "'null'::json", JSON, nil
	}
	switch value.kind {
	case hff.BytesType:
		return "to_json(" + postgresBase64(value.code) + ")", JSON, nil
	case hff.DurationType:
		return "to_json(" + b.durationTextInline(value.code) + ")", JSON, nil
	case hff.TimestampType:
		return "to_json(" + b.timestampUTCStringInline(value.code) + ")", JSON, nil
	case hff.FloatType:
		return b.floatText(value.code) + "::json", JSON, nil
	case hff.BoolType, hff.IntType, hff.StringType:
		return "to_json(" + value.code + ")", JSON, nil
	}
	return "", "", b.failure(n, "membership requires a supported JSON value representation")
}

// Null needles are search values. Only receiver-null terminates argument
// evaluation; a later no-value argument still propagates past an earlier null.
func membershipPresence(values []expression) string {
	args := "TRUE"
	for _, value := range values[1:] {
		args = both(args, value.present)
	}
	return both(values[0].present, "("+values[0].code+" IS NULL OR "+args+")")
}

func (b *builder) jsonIn(n hff.Node, values []expression) (expression, error) {
	receiver := values[0]
	if receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	if value, ok := nativeScalarIn(values); ok {
		return value, nil
	}
	left, lf, err := b.jsonRepresentation(n, receiver)
	if err != nil {
		return expression{}, err
	}
	l, _ := n.Child(0)
	var matches []string
	for i, arg := range values[1:] {
		r, _ := n.Child(i + 1)
		right, rf, err := b.jsonRepresentation(r, arg)
		if err != nil {
			return expression{}, err
		}
		equal, err := b.jsonEqual(n, left, lf, l.Type(), right, rf, r.Type())
		if err != nil {
			return expression{}, err
		}
		matches = append(matches, equal)
	}
	code := "(CASE WHEN " + receiver.code + " IS NULL THEN NULL ELSE (" + strings.Join(matches, " OR ") + ") END)"
	return expression{code, membershipPresence(values), hff.BoolType}, nil
}

// Native scalar values already have the same representation on both sides of
// equality. Avoid encoding each needle as JSON and decoding it again. Null
// needles never match a non-null receiver, while the receiver's own null still
// propagates as a method result. Argument no-value flags remain in presence.
func nativeScalarIn(values []expression) (expression, bool) {
	receiver := values[0]
	switch receiver.kind {
	case hff.BoolType, hff.IntType, hff.FloatType, hff.StringType, hff.BytesType:
	default:
		return expression{}, false
	}
	var needles []string
	for _, arg := range values[1:] {
		if arg.kind == hff.NullType {
			continue
		}
		if arg.kind != receiver.kind {
			return expression{}, false
		}
		code := arg.code
		if receiver.kind == hff.StringType {
			code += " COLLATE \"C\""
		}
		needles = append(needles, code)
	}
	match := "FALSE"
	if len(needles) > 0 {
		left := receiver.code
		if receiver.kind == hff.StringType {
			left += " COLLATE \"C\""
		}
		match = "COALESCE((" + left + " IN (" + strings.Join(needles, ",") + ")),FALSE)"
	}
	code := "(CASE WHEN " + receiver.code + " IS NULL THEN NULL ELSE " + match + " END)"
	return expression{code, membershipPresence(values), hff.BoolType}, true
}

func (b *builder) jsonMembership(n hff.Node, name string, values []expression, format JSONFormat) (expression, error) {
	receiver := values[0]
	if receiver.kind != hff.ListType {
		return expression{}, b.failure(n, "contains and unique require a list receiver")
	}
	r, _ := n.Child(0)
	// The runtime array branch may come from a finite union. Retain its
	// declared element schema; equality validates any nested ambiguity.
	list := sortListType(b.jsonStoredType(r))
	if list.Kind() != hff.ListType {
		return expression{}, b.failure(n, "membership requires a recoverable list schema")
	}
	element := list.Element()
	var code string
	if name == "unique" {
		la, ra := b.nextAlias(), b.nextAlias()
		equal, err := b.jsonItemEquality(n, la+".value", format, element, b.jsonItemsRoles(r), ra+".value", format, element, b.jsonItemsRoles(r))
		if err != nil {
			return expression{}, err
		}
		from := string(format) + "_array_elements(" + receiver.code + ") WITH ORDINALITY AS " + la + "(value,ord) CROSS JOIN " + string(format) + "_array_elements(" + receiver.code + ") WITH ORDINALITY AS " + ra + "(value,ord)"
		code = "(NOT EXISTS(SELECT 1 FROM " + from + " WHERE " + la + ".ord<" + ra + ".ord AND " + equal + "))"
	} else {
		var matches []string
		for i, arg := range values[1:] {
			child, _ := n.Child(i + 1)
			raw, rf, err := b.jsonRepresentation(child, arg)
			if err != nil {
				return expression{}, err
			}
			alias := b.nextAlias()
			equal, err := b.jsonItemEquality(n, alias+".value", format, element, b.jsonItemsRoles(r), raw, rf, b.jsonStoredType(child), b.jsonItemRoles(child))
			if err != nil {
				return expression{}, err
			}
			matches = append(matches, "EXISTS(SELECT 1 FROM "+string(format)+"_array_elements("+receiver.code+") AS "+alias+"(value) WHERE "+equal+")")
		}
		code = "(" + strings.Join(matches, " AND ") + ")"
	}
	code = "(CASE WHEN " + receiver.code + " IS NULL THEN NULL ELSE " + code + " END)"
	return expression{code, membershipPresence(values), hff.BoolType}, nil
}
