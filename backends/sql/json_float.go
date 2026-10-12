package sql

import (
	"strconv"
	"strings"
)

// JSON float spellings may underflow to signed zero even when PostgreSQL's
// float input routine rejects them. Compare valid decimal text to the exact
// half-subnormal before casting; numeric itself cannot hold arbitrary scales.
func jsonFloatCast(text string) string {
	half := strings.TrimLeft(strings.TrimPrefix(floatHalfMinimum, "0."), "0")
	// Explicit exponents larger than ten digits dominate every possible text
	// length in PostgreSQL. Saturation avoids overflowing an integer parser.
	exponent := "CASE WHEN length(e)>10 THEN CASE WHEN neg THEN -10000000000::bigint ELSE 10000000000::bigint END ELSE (CASE WHEN neg THEN -1 ELSE 1 END)*COALESCE(NULLIF(e,''),'0')::bigint END"
	prefix := "rpad(left(sig," + strconv.Itoa(len(half)) + ")," + strconv.Itoa(len(half)) + ",'0') COLLATE \"C\""
	tiny := "sig='' OR ord < -324 OR (ord=-324 AND (" + prefix + "<'" + half + "' OR (" + prefix + "='" + half + "' AND ltrim(substr(sig," + strconv.Itoa(len(half)+1) + "),'0')='')))"
	return "(SELECT CASE WHEN tiny THEN CASE WHEN left(raw,1)='-' THEN '-0'::double precision ELSE 0::double precision END ELSE (CASE WHEN tiny THEN NULL ELSE raw END)::double precision END FROM LATERAL (SELECT " + text + " AS raw OFFSET 0) AS __hff_float_raw CROSS JOIN LATERAL (SELECT regexp_replace(raw,'^[+-]','') AS unsigned OFFSET 0) AS __hff_float_unsigned CROSS JOIN LATERAL (SELECT split_part(lower(unsigned),'e',1) AS m,split_part(lower(unsigned),'e',2) AS exp OFFSET 0) AS __hff_float_parts CROSS JOIN LATERAL (SELECT replace(m,'.','') AS digits,CASE WHEN strpos(m,'.')=0 THEN length(m) ELSE strpos(m,'.')-1 END AS before,ltrim(regexp_replace(exp,'^[+-]',''),'0') AS e,left(exp,1)='-' AS neg OFFSET 0) AS __hff_float_digits CROSS JOIN LATERAL (SELECT ltrim(digits,'0') AS sig, (" + exponent + ")+before-(length(digits)-length(ltrim(digits,'0')))-1 AS ord OFFSET 0) AS __hff_float_order CROSS JOIN LATERAL (SELECT (" + tiny + ") AS tiny OFFSET 0) AS __hff_float_tiny)"
}
