package sql

import "github.com/ornew/hff"

// Dynamic index arguments retain their declared numeric/string identity.
// Incompatible runtime values return ordinary null; explicit guards retain
// their separate no-value presence. Numeric casts remain within their family.
func (b *builder) jsonDynamicIndex(n hff.Node, receiver, index expression, format JSONFormat, present string, stored hff.Type) (expression, error) {
	argument, _ := n.Child(1)
	raw, physical, err := b.jsonRepresentation(argument, index)
	if err != nil {
		return expression{}, err
	}
	parent, _ := n.Child(0)
	shape := makeComparisonShape(b.jsonStoredType(parent))
	var numeric jsonNumericOperand
	if shape.any || shape.list {
		numeric, err = b.numericOperand(argument, index)
		if err != nil {
			return expression{}, err
		}
	}
	class := string(format) + "_typeof(" + receiver.code + ")"
	code := "CASE"
	if numeric.integerPossible {
		valid := both(numeric.isInteger, validJSONIndex(numeric.integer))
		code += " WHEN " + both(class+"='array'", valid) + " THEN " + receiver.code + " -> " + numeric.integer.code + "::integer"
	}
	if numeric.floatPossible {
		valid := both(numeric.isFloat, validJSONIndex(numeric.floating))
		code += " WHEN " + both(class+"='array'", valid) + " THEN " + receiver.code + " -> " + numeric.floating.code + "::integer"
	}
	key := "(" + raw + " #>> '{}'::text[])"
	code += " WHEN " + both(class+"='object'", b.jsonStringMatch(argument, raw, physical)) + " THEN " + receiver.code + " -> " + key + "::text ELSE NULL::" + string(format) + " END"
	return b.jsonDecode(n, "("+code+")", format, stored, present)
}
