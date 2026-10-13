package sql

import (
	"strings"

	"github.com/ornew/hff"
)

type jsonNumericOperand struct {
	integer, floating              expression
	isInteger, isFloat             string
	integerPossible, floatPossible bool
}

type numericBranch struct {
	condition string
	value     expression
}

func (b *builder) numericOperand(n hff.Node, value expression) (jsonNumericOperand, error) {
	out := jsonNumericOperand{integer: expression{"NULL::bigint", value.present, hff.IntType}, floating: expression{"NULL::double precision", value.present, hff.FloatType}, isInteger: "FALSE", isFloat: "FALSE"}
	switch value.kind {
	case hff.IntType:
		out.integer, out.isInteger, out.integerPossible = value, "TRUE", true
		out.floating.code = "(" + value.code + "::double precision)"
		return out, nil
	case hff.FloatType:
		out.floating, out.isFloat, out.floatPossible = value, "TRUE", true
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
	storedInt, storedFloat := jsonNumericCandidates(b.jsonStoredType(n))
	if storedInt && storedFloat && format != JSON {
		return out, b.failure(n, "dynamic arithmetic requires preserved numeric types or type metadata")
	}
	// Stored families define the representation and discriminator contract;
	// the compiled type limits which of those families can reach this value.
	// Validate storage before pruning a type guard's unreachable alternatives.
	compiledInt, compiledFloat := jsonNumericCandidates(n.Type())
	out.integerPossible, out.floatPossible = storedInt && compiledInt, storedFloat && compiledFloat
	class := "(" + string(format) + "_typeof(" + value.code + ")='number')"
	text := "(" + value.code + " #>> '{}'::text[])"
	if out.integerPossible {
		out.isInteger = class
		if storedInt && storedFloat {
			out.isInteger = both(class, text+" !~ '[.eE]'")
		}
		out.integer.code = "(CASE WHEN " + out.isInteger + " THEN " + jsonScalarCast(text, hff.IntType) + " ELSE NULL::bigint END)"
	}
	if out.floatPossible {
		out.isFloat = class
		if storedInt && storedFloat {
			out.isFloat = both(class, text+" ~ '[.eE]'")
		}
	}
	// The Float view also promotes actual Int rows when a mixed operation
	// needs it. Callers omit that view when no reachable Float branch exists.
	if out.integerPossible || out.floatPossible {
		out.floating.code = "(CASE WHEN " + out.numericCondition() + " THEN " + jsonScalarCast(text, hff.FloatType) + " ELSE NULL::double precision END)"
	}
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
	var branches []numericBranch
	integers := a.integerPossible && c.integerPossible
	bothIntegers := both(a.isInteger, c.isInteger)
	if integers {
		integer, err := b.arithmetic(n, a.integer, c.integer, op)
		if err != nil {
			return expression{}, err
		}
		branches = append(branches, numericBranch{bothIntegers, integer})
	}
	floating := (a.integerPossible || a.floatPossible) && (c.integerPossible || c.floatPossible) && (a.floatPossible || c.floatPossible)
	if op != hff.Remainder && floating {
		if integers {
			// Actual Int/Int rows use the checked integer operation. Mask
			// those rows before native Float arithmetic can evaluate them.
			a.floating.code = "(CASE WHEN " + bothIntegers + " THEN NULL::double precision ELSE " + a.floating.code + " END)"
			c.floating.code = "(CASE WHEN " + bothIntegers + " THEN NULL::double precision ELSE " + c.floating.code + " END)"
		}
		value, err := b.floatArithmetic(n, a.floating, c.floating, op)
		if err != nil {
			return expression{}, err
		}
		branches = append(branches, numericBranch{both(a.numericCondition(), c.numericCondition()), value})
	}
	// A nonnumeric left value does not skip the right operand. Only actual
	// null/no-value propagation can settle evaluation before its type check.
	return b.numericResult(n, branches, ordinaryPresence([]expression{left, right}))
}

func (b *builder) dynamicJSONNegation(n hff.Node, value expression) (expression, error) {
	child, _ := n.Child(0)
	operand, err := b.numericOperand(child, value)
	if err != nil {
		return expression{}, err
	}
	var branches []numericBranch
	if operand.integerPossible {
		integer := "(CASE WHEN " + operand.integer.code + "='-9223372036854775808'::bigint THEN NULL::bigint ELSE -(" + operand.integer.code + ") END)"
		branches = append(branches, numericBranch{operand.isInteger, expression{integer, value.present, hff.IntType}})
	}
	if operand.floatPossible {
		floating := "(-(" + operand.floating.code + "))"
		branches = append(branches, numericBranch{operand.isFloat, expression{floating, value.present, hff.FloatType}})
	}
	return b.numericResult(n, branches, value.present)
}

func (operand jsonNumericOperand) numericCondition() string {
	if !operand.integerPossible {
		return operand.isFloat
	}
	if !operand.floatPossible {
		return operand.isInteger
	}
	return "(" + operand.isInteger + " OR " + operand.isFloat + ")"
}

func numericJSONBranches(branches []string) string {
	if len(branches) == 0 {
		return "NULL::json"
	}
	return "(CASE " + strings.Join(branches, " ") + " ELSE NULL::json END)"
}

// A single compiled numeric result family already has the carrier that JSON
// decoding would restore. Keep that native value rather than formatting and
// reparsing it. Storage validation and branch arithmetic happen before this
// helper; result presence belongs to the original operands, not their numeric
// views. Numeric unions and Null/Never keep the existing JSON decoding path.
func (b *builder) numericResult(n hff.Node, branches []numericBranch, present string) (expression, error) {
	kind, native := jsonKind(n.Type())
	native = native && (kind == hff.IntType || kind == hff.FloatType)
	for _, branch := range branches {
		if branch.value.kind != kind {
			native = false
		}
	}
	if native {
		carrier := "bigint"
		if kind == hff.FloatType {
			carrier = "double precision"
		}
		code := "NULL::" + carrier
		if len(branches) > 0 {
			var cases []string
			for _, branch := range branches {
				cases = append(cases, "WHEN "+branch.condition+" THEN "+branch.value.code)
			}
			code = "(CASE " + strings.Join(cases, " ") + " ELSE " + code + " END)"
		}
		return expression{code, present, kind}, nil
	}
	var cases []string
	for _, branch := range branches {
		code, _, err := b.jsonRepresentation(n, branch.value)
		if err != nil {
			return expression{}, err
		}
		cases = append(cases, "WHEN "+branch.condition+" THEN "+code)
	}
	return b.jsonDecode(n, numericJSONBranches(cases), JSON, n.Type(), present)
}
