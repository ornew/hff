package sql

import "github.com/ornew/hff"

type jsonNumericOperand struct {
	integer, floating  expression
	isInteger, isFloat string
}

func (b *builder) numericOperand(n hff.Node, value expression) (jsonNumericOperand, error) {
	out := jsonNumericOperand{integer: expression{"NULL::bigint", value.present, hff.IntType}, floating: expression{"NULL::double precision", value.present, hff.FloatType}, isInteger: "FALSE", isFloat: "FALSE"}
	switch value.kind {
	case hff.IntType:
		out.integer, out.isInteger = value, "TRUE"
		out.floating.code = "(" + value.code + "::double precision)"
		return out, nil
	case hff.FloatType:
		out.floating, out.isFloat = value, "TRUE"
		return out, nil
	case hff.NullType:
		return out, nil
	case hff.AnyType:
	default:
		return out, nil
	}
	format, ok := b.jsonValues[value.code]
	if !ok {
		return out, b.failure(n, "dynamic arithmetic requires recoverable JSON numbers")
	}
	integer, floating := jsonNumericCandidates(b.jsonStoredType(n))
	if integer && floating && format != JSON {
		return out, b.failure(n, "dynamic arithmetic requires preserved numeric types or type metadata")
	}
	class := "(" + string(format) + "_typeof(" + value.code + ")='number')"
	text := "(" + value.code + " #>> '{}'::text[])"
	if integer {
		out.isInteger = class
	}
	if floating {
		out.isFloat = class
	}
	if integer && floating {
		out.isInteger = both(class, text+" !~ '[.eE]'")
		out.isFloat = both(class, text+" ~ '[.eE]'")
	}
	out.integer.code = "(CASE WHEN " + out.isInteger + " THEN " + jsonScalarCast(text, hff.IntType) + " ELSE NULL::bigint END)"
	numeric := "(" + out.isInteger + " OR " + out.isFloat + ")"
	out.floating.code = "(CASE WHEN " + numeric + " THEN " + jsonScalarCast(text, hff.FloatType) + " ELSE NULL::double precision END)"
	return out, nil
}

func (b *builder) dynamicJSONArithmetic(n hff.Node, left, right expression, op hff.Operator) (expression, error) {
	l, _ := n.Child(0)
	r, _ := n.Child(1)
	a, err := b.numericOperand(l, left)
	if err != nil {
		return expression{}, err
	}
	c, err := b.numericOperand(r, right)
	if err != nil {
		return expression{}, err
	}
	bothIntegers := both(a.isInteger, c.isInteger)
	integer, err := b.arithmetic(n, a.integer, c.integer, op)
	if err != nil {
		return expression{}, err
	}
	intJSON, _, err := b.jsonRepresentation(n, integer)
	if err != nil {
		return expression{}, err
	}
	code := "CASE WHEN " + bothIntegers + " THEN " + intJSON
	if op != hff.Remainder {
		// Integer rows use the checked integer branch. Mask unused float
		// inputs before the more expensive exact binary conversion.
		a.floating.code = "(CASE WHEN " + bothIntegers + " THEN NULL::double precision ELSE " + a.floating.code + " END)"
		c.floating.code = "(CASE WHEN " + bothIntegers + " THEN NULL::double precision ELSE " + c.floating.code + " END)"
		floating, err := b.floatArithmetic(n, a.floating, c.floating, op)
		if err != nil {
			return expression{}, err
		}
		floatJSON, _, err := b.jsonRepresentation(n, floating)
		if err != nil {
			return expression{}, err
		}
		numbers := both("("+a.isInteger+" OR "+a.isFloat+")", "("+c.isInteger+" OR "+c.isFloat+")")
		code += " WHEN " + numbers + " THEN " + floatJSON
	}
	code = "(" + code + " ELSE NULL::json END)"
	// A nonnumeric left value does not skip the right operand. Only actual
	// null/no-value propagation can settle evaluation before its type check.
	return b.jsonDecode(n, code, JSON, n.Type(), ordinaryPresence([]expression{left, right}))
}

func (b *builder) dynamicJSONNegation(n hff.Node, value expression) (expression, error) {
	child, _ := n.Child(0)
	operand, err := b.numericOperand(child, value)
	if err != nil {
		return expression{}, err
	}
	integer := "(CASE WHEN " + operand.integer.code + "='-9223372036854775808'::bigint THEN NULL::bigint ELSE -(" + operand.integer.code + ") END)"
	floating := "(-(" + operand.floating.code + "))"
	code := "(CASE WHEN " + operand.isInteger + " THEN to_json(" + integer + ") WHEN " + operand.isFloat + " THEN " + b.floatText(floating) + "::json ELSE NULL::json END)"
	return b.jsonDecode(n, code, JSON, n.Type(), value.present)
}
