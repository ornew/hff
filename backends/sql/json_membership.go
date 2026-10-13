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

func (b *builder) jsonMembership(n hff.Node, name string, values []expression, format JSONFormat) (expression, error) {
	receiver := values[0]
	if receiver.kind != hff.ListType {
		return expression{}, b.failure(n, "contains and unique require a list receiver")
	}
	r, _ := n.Child(0)
	// Nullable has one concrete list member; the container kind was resolved
	// before this call. Reuse that member to obtain the precise element type.
	members, err := b.jsonEqualityMembers(n, r.Type())
	if err != nil || len(members) != 1 {
		if err == nil {
			err = b.failure(n, "membership requires an unambiguous list schema")
		}
		return expression{}, err
	}
	element := members[0].Element()
	var code string
	if name == "unique" {
		la, ra := b.nextAlias(), b.nextAlias()
		equal, err := b.jsonEqual(n, la+".value", format, element, ra+".value", format, element)
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
			equal, err := b.jsonEqual(n, alias+".value", format, element, raw, rf, child.Type())
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
