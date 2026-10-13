package sql

import "github.com/ornew/hff"

func (b *builder) roundedFloat(value, name string) string {
	// PostgreSQL's native Float builtins preserve the Float type. In
	// particular, round() follows the backend's midpoint rule rather than
	// reconstructing an exact decimal value to emulate another runtime.
	return name + "(" + value + ")"
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
