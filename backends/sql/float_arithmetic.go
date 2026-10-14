package sql

import "github.com/ornew/hff"

// floatOperands shares native Float operands within the current projection
// scope. Presence remains owned by each operation; no exact decomposition is
// needed for backend-native arithmetic.
func (b *builder) floatOperands(left, right string) (string, string) {
	operands := [2]struct{ source, value string }{{source: left}, {source: right}}
	if b.sharedPaths == nil {
		b.sharedPaths = make(map[string]expression)
	}
	for i := range operands {
		operand := &operands[i]
		key := "float-arithmetic:" + operand.source + ":value"
		if cached, ok := b.sharedPaths[key]; ok {
			operand.value = cached.code
		} else {
			operand.value = b.project(operand.source)
			b.sharedPaths[key] = expression{operand.value, "TRUE", hff.FloatType}
		}
	}
	return operands[0].value, operands[1].value
}

func floatSign(value string) string { return "(get_byte(float8send(" + value + "),0)>=128)" }

func (b *builder) floatArithmetic(n hff.Node, left, right expression, op hff.Operator) (expression, error) {
	present := ordinaryPresence([]expression{left, right})
	if op == hff.Remainder {
		return expression{"NULL::double precision", present, hff.FloatType}, nil
	}
	l, r := left.code, right.code
	if left.kind == hff.IntType {
		l = "(" + l + "::double precision)"
	}
	if right.kind == hff.IntType {
		r = "(" + r + "::double precision)"
	}
	l, r = b.floatOperands(l, r)
	leftMagnitude, rightMagnitude := "abs("+l+")", "abs("+r+")"
	var safe string
	switch op {
	case hff.Add, hff.Subtract:
		safe = both(leftMagnitude+"<=1e300::double precision", rightMagnitude+"<=1e300::double precision")
		// Cancellation cannot overflow, and a nonzero difference of finite
		// binary64 operands cannot fall below the smallest subnormal.
		if op == hff.Add {
			safe = "(" + safe + " OR (" + l + ">=0 AND " + r + "<=0) OR (" + l + "<=0 AND " + r + ">=0))"
		} else {
			safe = "(" + safe + " OR (" + l + ">=0 AND " + r + ">=0) OR (" + l + "<=0 AND " + r + "<=0))"
		}
	case hff.Multiply, hff.Divide:
		safe = both(leftMagnitude+" BETWEEN 1e-100::double precision AND 1e100::double precision", rightMagnitude+" BETWEEN 1e-100::double precision AND 1e100::double precision")
		if op == hff.Multiply {
			safe = "(" + safe + " OR " + l + "=0 OR " + r + "=0)"
		} else {
			safe = "(" + safe + " OR " + l + "=0)"
		}
	}
	nativeRight, decimalRight := r, "("+r+")::text::numeric"
	if op == hff.Divide {
		nativeRight, decimalRight = "NULLIF("+r+",0)", "NULLIF("+decimalRight+",0)"
	}
	native := "(" + l + " " + string(op) + " " + nativeRight + ")"
	// PostgreSQL arithmetic can raise on overflow or underflow. At extreme
	// magnitudes, use its ordinary decimal arithmetic and validate the result
	// before Float input. Text conversion avoids Float->numeric's DBL_DIG
	// truncation. This fallback follows backend/session precision, not a
	// portable exact binary64 rounding algorithm, and may lose zero's sign.
	alias := b.nextAlias()
	decimal := "(" + l + ")::text::numeric " + string(op) + " " + decimalRight
	fallback := "(SELECT " + nativeFloatCast(alias+".v::text") + " FROM LATERAL (SELECT (" + decimal + ") AS v OFFSET 0) AS " + alias + ")"
	code := "(CASE WHEN " + l + " IS NULL OR " + r + " IS NULL THEN NULL::double precision WHEN " + safe + " THEN " + native + " ELSE " + fallback + " END)"
	return expression{code, present, hff.FloatType}, nil
}
