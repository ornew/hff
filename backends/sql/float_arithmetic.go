package sql

import (
	"strconv"

	"github.com/ornew/hff"
)

// binaryNumeric evaluates significand*2^power without decimal rounding. Integer
// powers of five and decimal placement also cover the smallest subnormal.
func (b *builder) binaryNumeric(significand, power string) string {
	parts, integer := b.nextAlias(), b.nextAlias()
	p, digits := parts+".power", integer+".digits"
	decimal := "CASE WHEN " + p + ">=0 THEN " + digits + " ELSE CASE WHEN length(" + digits + ")<=-" + p + " THEN '0.'||repeat('0',-" + p + "-length(" + digits + "))||" + digits + " ELSE left(" + digits + ",length(" + digits + ")+" + p + ")||'.'||substr(" + digits + ",length(" + digits + ")+" + p + "+1) END END"
	return "(SELECT (" + decimal + ")::numeric FROM LATERAL (SELECT " + power + " AS power," + significand + " AS significand OFFSET 0) AS " + parts + " CROSS JOIN LATERAL (SELECT trunc(CASE WHEN " + p + ">=0 THEN " + parts + ".significand*power(2::numeric," + p + ") ELSE " + parts + ".significand*power(5::numeric,-" + p + ") END)::text AS digits OFFSET 0) AS " + integer + ")"
}

func (b *builder) floatNumeric(value string) string {
	bits := b.nextAlias()
	bytes := bits + ".bytes"
	e := "(get_byte(" + bytes + ",0)%128*16+get_byte(" + bytes + ",1)/16)"
	m := "(get_byte(" + bytes + ",1)%16)::numeric*281474976710656"
	for i, weight := range []string{"1099511627776", "4294967296", "16777216", "65536", "256", "1"} {
		m += "+get_byte(" + bytes + "," + strconv.Itoa(2+i) + ")::numeric*" + weight
	}
	power := "(CASE WHEN " + e + "=0 THEN -1074 ELSE " + e + "-1075 END)"
	significand := "(" + m + "+CASE WHEN " + e + "=0 THEN 0 ELSE 4503599627370496 END)"
	exact := b.binaryNumeric(significand, power)
	return "(SELECT (CASE WHEN get_byte(" + bytes + ",0)>=128 THEN -1 ELSE 1 END)*" + exact + " FROM LATERAL (SELECT float8send(" + value + ") AS bytes OFFSET 0) AS " + bits + ")"
}

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
