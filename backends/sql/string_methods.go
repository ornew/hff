package sql

import (
	"strings"

	"github.com/ornew/hff"
)

func (b *builder) stringCall(n hff.Node, name string, values []expression) (expression, error) {
	receiver := values[0]
	present := ordinaryPresence(values)
	if receiver.kind != hff.StringType {
		return expression{}, b.failure(n, "method requires a supported scalar SQL receiver")
	}
	for _, arg := range values[1:] {
		if arg.kind != hff.StringType && arg.kind != hff.NullType {
			return expression{}, b.failure(n, "method requires string SQL arguments")
		}
	}
	code := receiver.code
	switch name {
	case "len":
		return expression{"char_length(" + code + ")::bigint", present, hff.IntType}, nil
	case "concat":
		return expression{"(" + code + " || " + values[1].code + ")", present, hff.StringType}, nil
	case "contains":
		parts := make([]string, len(values)-1)
		for i, arg := range values[1:] {
			parts[i] = "(strpos(" + code + " COLLATE \"C\", " + arg.code + " COLLATE \"C\") > 0)"
		}
		// Explicit null propagation matters when another argument does not match.
		nulls := make([]string, len(values))
		for i, value := range values {
			nulls[i] = value.code + " IS NULL"
		}
		return expression{"(CASE WHEN " + strings.Join(nulls, " OR ") + " THEN NULL ELSE (" + strings.Join(parts, " AND ") + ") END)", present, hff.BoolType}, nil
	case "startswith", "endswith":
		fn := "left"
		if name == "endswith" {
			fn = "right"
		}
		arg := values[1].code
		return expression{"(" + fn + "(" + code + ", char_length(" + arg + ")) COLLATE \"C\" = " + arg + " COLLATE \"C\")", present, hff.BoolType}, nil
	default:
		return expression{}, b.failure(n, "unsupported string method")
	}
}
