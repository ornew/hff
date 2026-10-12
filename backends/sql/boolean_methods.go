package sql

import "github.com/ornew/hff"

func (b *builder) booleanCall(n hff.Node, name string, values []expression) (expression, error) {
	receiver := values[0]
	present := ordinaryPresence(values)
	var code string
	switch name {
	case "not":
		code = "(NOT " + receiver.code + ")"
	case "and", "or":
		// HFF short-circuit and/or skip the argument (including no-value)
		// when the receiver already determines the result.
		arg := values[1]
		if arg.kind != hff.BoolType && arg.kind != hff.NullType {
			return expression{}, b.failure(n, "unsupported boolean method argument")
		}
		op, decisive := "AND", "FALSE"
		if name == "or" {
			op, decisive = "OR", "TRUE"
		}
		code = "(CASE WHEN " + receiver.code + " IS NULL THEN NULL ELSE (" + receiver.code + " " + op + " " + arg.code + ") END)"
		present = both(receiver.present, "("+receiver.code+" IS NULL OR "+receiver.code+" IS "+decisive+" OR "+arg.present+")")
	default:
		return expression{}, b.failure(n, "unsupported boolean method")
	}
	return expression{code, present, hff.BoolType}, nil
}
