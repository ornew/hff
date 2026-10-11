package sql

import "strconv"

// floatText constructs the exact decimal value from IEEE 754 bits, then picks
// the first nearest, ties-to-even decimal that round-trips. PostgreSQL's float
// text output deliberately avoids some midpoint decimals and can depend on
// extra_float_digits; neither is the HFF canonical representation.
// Adjacent decimal candidates are necessary at powers of two, whose lower
// and upper rounding intervals have different widths.
func (b *builder) floatText(value string) string {
	bits, parts, integer, exact, exponent := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	precision, rounded, candidate, selected, spelling, neighbor := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	bytes := bits + ".bytes"
	e := "(get_byte(" + bytes + ",0)%128*16+get_byte(" + bytes + ",1)/16)"
	m := "(get_byte(" + bytes + ",1)%16)::numeric*281474976710656"
	for i, weight := range []string{"1099511627776", "4294967296", "16777216", "65536", "256", "1"} {
		m += "+get_byte(" + bytes + "," + strconv.Itoa(2+i) + ")::numeric*" + weight
	}
	power := parts + ".power"
	significand := parts + ".significand"
	digits := integer + ".digits"
	x := exact + ".value"
	exp := exponent + ".value"
	scale := "(" + precision + ".digits-1-" + exp + ")"
	unit := "(CASE WHEN " + scale + ">0 THEN ('0.'||repeat('0'," + scale + "-1)||'1')::numeric ELSE power(10::numeric,-" + scale + ") END)"
	r := rounded + ".value"
	c := candidate + ".value"
	text := selected + ".value::text"
	decimalExponent := func(text string) string {
		return "(CASE WHEN split_part(" + text + ",'.',1)<>'0' THEN length(split_part(" + text + ",'.',1))-1 ELSE -1-length(split_part(" + text + ",'.',2))+length(ltrim(split_part(" + text + ",'.',2),'0')) END)"
	}
	short := spelling + ".digits"
	finalExp := spelling + ".exponent"
	fixed := "(CASE WHEN strpos(" + text + ",'.')>0 THEN rtrim(rtrim(" + text + ",'0'),'.') ELSE " + text + " END)"
	formatted := "(CASE WHEN " + finalExp + " < -4 OR " + finalExp + " >= 6 THEN left(" + short + ",1) || (CASE WHEN length(" + short + ")>1 THEN '.'||substr(" + short + ",2) ELSE '' END) || 'e' || (CASE WHEN " + finalExp + ">=0 THEN '+' ELSE '-' END) || lpad(abs(" + finalExp + ")::text,greatest(2,length(abs(" + finalExp + ")::text)),'0') ELSE " + fixed + " || (CASE WHEN strpos(" + fixed + ",'.')=0 THEN '.0' ELSE '' END) END)"
	from := " FROM LATERAL (SELECT float8send(" + value + ") AS bytes OFFSET 0) AS " + bits
	from += " CROSS JOIN LATERAL (SELECT (CASE WHEN " + e + "=0 THEN -1074 ELSE " + e + "-1075 END) AS power,(" + m + "+CASE WHEN " + e + "=0 THEN 0 ELSE 4503599627370496 END) AS significand OFFSET 0) AS " + parts
	from += " CROSS JOIN LATERAL (SELECT trunc(CASE WHEN " + power + ">=0 THEN " + significand + "*power(2::numeric," + power + ") ELSE " + significand + "*power(5::numeric,-" + power + ") END)::text AS digits OFFSET 0) AS " + integer
	from += " CROSS JOIN LATERAL (SELECT (CASE WHEN " + power + ">=0 THEN " + digits + " ELSE CASE WHEN length(" + digits + ")<=-" + power + " THEN '0.'||repeat('0',-" + power + "-length(" + digits + "))||" + digits + " ELSE left(" + digits + ",length(" + digits + ")+" + power + ")||'.'||substr(" + digits + ",length(" + digits + ")+" + power + "+1) END END)::numeric AS value OFFSET 0) AS " + exact
	from += " CROSS JOIN LATERAL (SELECT " + decimalExponent(x+"::text") + " AS value OFFSET 0) AS " + exponent
	from += " CROSS JOIN LATERAL (SELECT " + c + " AS value FROM generate_series(1,17) AS " + precision + "(digits) CROSS JOIN LATERAL (SELECT round(" + x + "," + scale + ") AS value OFFSET 0) AS " + rounded
	from += " CROSS JOIN generate_series(-1,1) AS " + neighbor + "(delta) CROSS JOIN LATERAL (SELECT (" + r + "+" + neighbor + ".delta*" + unit + ") AS value OFFSET 0) AS " + candidate
	// Avoid PostgreSQL exceptions for decimal candidates outside the finite
	// float input interval. Every finite float has a 17-digit candidate inside it.
	from += " WHERE CASE WHEN " + c + ">=3e-324::numeric AND " + c + "<=1.7976931348623158e308::numeric THEN " + c + "::text::double precision=abs(" + value + ") ELSE FALSE END ORDER BY " + precision + ".digits,abs(" + c + "-" + x + "),mod(" + c + "/" + unit + ",2) LIMIT 1 OFFSET 0) AS " + selected
	from += " CROSS JOIN LATERAL (SELECT rtrim(ltrim(replace(" + text + ",'.',''),'0'),'0') AS digits," + decimalExponent(text) + " AS exponent OFFSET 0) AS " + spelling
	sign := "(CASE WHEN get_byte(float8send(" + value + "),0)>=128 THEN '-' ELSE '' END)"
	return "(CASE WHEN " + value + " IS NULL THEN 'null' WHEN " + value + "=0 THEN " + sign + "||'0.0' ELSE (SELECT " + sign + "||" + formatted + from + ") END)"
}
