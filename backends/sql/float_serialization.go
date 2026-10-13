package sql

// Float text follows PostgreSQL's native output model and session precision
// settings. Keep a decimal marker so JSON decoding retains the Float subtype,
// and preserve signed zero independently of the database's zero spelling.
func (b *builder) floatText(value string) string {
	text := "(" + value + ")::text"
	return "(CASE WHEN " + value + " IS NULL THEN 'null' WHEN " + value + "=0 THEN CASE WHEN " + floatSign(value) + " THEN '-0.0' ELSE '0.0' END WHEN " + text + " ~ '[.eE]' THEN " + text + " ELSE " + text + "||'.0' END)"
}
