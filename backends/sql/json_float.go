package sql

// JSON Float decoding uses PostgreSQL's native double-precision input model.
// Its input validator rejects overflow and values too small to distinguish
// from zero; these evaluation failures become ordinary null. Numeric syntax
// is validated by the caller before either entry point is used.
func jsonFloatCast(text string) string { return nativeFloatCast(text) }

func parseJSONFloat(text string) string { return nativeFloatCast(text) }

// Keep the cast in the validated branch so rejected input cannot raise a
// database exception. PostgreSQL 16 or later supplies pg_input_is_valid.
func nativeFloatCast(text string) string {
	return "(CASE WHEN pg_input_is_valid(" + text + ",'double precision') THEN (" + text + ")::double precision ELSE NULL::double precision END)"
}
