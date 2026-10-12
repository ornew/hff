package sql

import "github.com/ornew/hff"

// Schema-known integers are exact values, independent of storage spelling.
// numeric preserves the full int64 range before the integer cast. Float
// parsing handles underflow before using the original spelling for rounding.
func jsonScalarCast(text string, kind hff.TypeKind) string {
	if kind == hff.IntType {
		return "(" + text + "::numeric::bigint)"
	}
	if kind == hff.FloatType {
		return jsonFloatCast(text)
	}
	return "(" + text + "::" + sqlType(kind) + ")"
}
