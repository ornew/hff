package sql

import "github.com/ornew/hff"

// Schema-known integers are exact values, independent of storage spelling.
// numeric preserves the full int64 range before the integer cast; floats must
// remain direct casts to preserve their IEEE representation and signed zero.
func jsonScalarCast(text string, kind hff.TypeKind) string {
	if kind == hff.IntType {
		return "(" + text + "::numeric::bigint)"
	}
	return "(" + text + "::" + sqlType(kind) + ")"
}
