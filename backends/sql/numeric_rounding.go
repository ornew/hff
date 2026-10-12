package sql

import "github.com/ornew/hff"

func (b *builder) roundedFloat(value, name string) string {
	value = b.project(value)
	// PostgreSQL rounds numeric ties away from zero. Decode binary64 exactly
	// first: casting float to numeric would round its decimal representation.
	rounded := b.project(name + "(" + b.floatNumeric(value) + ")")
	return "(CASE WHEN " + rounded + " IS NULL THEN NULL::double precision WHEN " + rounded + "=0 AND " + floatSign(value) + " THEN '-0'::double precision ELSE " + rounded + "::double precision END)"
}

func (b *builder) numericRounding(n hff.Node, name string, value expression) (expression, error) {
	switch value.kind {
	case hff.IntType, hff.NullType:
		return value, nil
	case hff.FloatType:
		return expression{b.roundedFloat(value.code, name), value.present, hff.FloatType}, nil
	}
	child, _ := n.Child(0)
	operand, err := b.numericOperand(child, value)
	if err != nil {
		return expression{}, err
	}
	floating := b.roundedFloat(operand.floating.code, name)
	code := "(CASE WHEN " + operand.isInteger + " THEN to_json(" + operand.integer.code + ") WHEN " + operand.isFloat + " THEN " + b.floatText(floating) + "::json ELSE NULL::json END)"
	return b.jsonDecode(n, code, JSON, n.Type(), value.present)
}
