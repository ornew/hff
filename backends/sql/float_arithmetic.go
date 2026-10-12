package sql

import (
	"math/big"
	"strconv"

	"github.com/ornew/hff"
)

// The overflow midpoint and half the minimum subnormal are exact binary64
// rounding boundaries. Numeric division must not be used to construct them.
var floatOverflowMidpoint = func() string {
	upper := new(big.Int).Lsh(big.NewInt(1), 1024)
	half := new(big.Int).Lsh(big.NewInt(1), 970)
	return upper.Sub(upper, half).String()
}()

var floatHalfMinimum = func() string {
	denominator := new(big.Int).Lsh(big.NewInt(1), 1075)
	return new(big.Rat).SetFrac(big.NewInt(1), denominator).FloatString(1075)
}()

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
	l, r = b.project(l), b.project(r)
	ln, rn := b.project(b.floatNumeric(l)), b.project(b.floatNumeric(r))
	negative := "(" + floatSign(l) + " <> " + floatSign(r) + ")"
	if op == hff.Divide {
		return expression{b.floatQuotient(ln, rn, negative), present, hff.FloatType}, nil
	}
	x := b.project("(" + ln + " " + string(op) + " " + rn + ")")
	if op == hff.Add {
		negative = "(" + x + "<0 OR (" + x + "=0 AND " + floatSign(l) + " AND " + floatSign(r) + "))"
	} else if op == hff.Subtract {
		negative = "(" + x + "<0 OR (" + x + "=0 AND " + floatSign(l) + " AND NOT " + floatSign(r) + "))"
	}
	zero := "(CASE WHEN " + negative + " THEN '-0'::double precision ELSE 0::double precision END)"
	code := "(CASE WHEN " + x + " IS NULL THEN NULL::double precision WHEN abs(" + x + ")>=" + floatOverflowMidpoint + "::numeric THEN NULL::double precision WHEN abs(" + x + ")<=" + floatHalfMinimum + "::numeric THEN " + zero + " ELSE " + x + "::double precision END)"
	return expression{code, present, hff.FloatType}, nil
}

// A 1000-place decimal quotient is farther from every non-tie rounding
// midpoint than its approximation error. Exact cross-products identify ties
// and replace an odd candidate with its even adjacent float.
func (b *builder) floatQuotient(left, right, negative string) string {
	n, d := b.project("abs("+left+")"), b.project("abs("+right+")")
	low := n + "<=" + d + "*" + floatHalfMinimum + "::numeric"
	high := n + ">=" + d + "*" + floatOverflowMidpoint + "::numeric"
	quotient := b.project("((" + n + "+0::numeric(1000,1000))/NULLIF(" + d + ",0))")
	candidate := b.project("(CASE WHEN " + low + " OR " + high + " THEN NULL::double precision ELSE " + quotient + "::double precision END)")
	exact := b.project(b.floatNumeric(candidate))
	bytes := "float8send(" + candidate + ")"
	e := "(get_byte(" + bytes + ",0)%128*16+get_byte(" + bytes + ",1)/16)"
	power := "(CASE WHEN " + e + "=0 THEN -1074 ELSE " + e + "-1075 END)"
	unit := b.project(b.binaryNumeric("1::numeric", power))
	odd := "(get_byte(" + bytes + ",7)%2=1)"
	lower := n + "=" + d + "*(" + exact + "-" + unit + "*0.5::numeric)"
	upper := n + "=" + d + "*(" + exact + "+" + unit + "*0.5::numeric)"
	rounded := "(CASE WHEN " + odd + " AND " + lower + " THEN (" + exact + "-" + unit + ")::double precision WHEN " + odd + " AND " + upper + " THEN (" + exact + "+" + unit + ")::double precision ELSE " + candidate + " END)"
	signed := "(CASE WHEN " + negative + " THEN -(" + rounded + ") ELSE " + rounded + " END)"
	zero := "(CASE WHEN " + negative + " THEN '-0'::double precision ELSE 0::double precision END)"
	return "(CASE WHEN " + d + " IS NULL OR " + d + "=0 OR " + n + " IS NULL OR " + high + " THEN NULL::double precision WHEN " + low + " THEN " + zero + " ELSE " + signed + " END)"
}
