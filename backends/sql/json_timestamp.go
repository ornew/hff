package sql

// jsonTimestampNanoseconds converts a schema-known canonical UTC RFC 3339
// string to exact epoch nanoseconds in PostgreSQL numeric. The caller's JSON
// storage contract guarantees a valid canonical string with at most nine
// fractional digits. Parsing only the whole-second portion as timestamptz
// avoids PostgreSQL's microsecond rounding of the fractional digits.
func jsonTimestampNanoseconds(text string) string {
	// PostgreSQL numbers BC years without a year zero. HFF year 0000 is 1 BC.
	whole := "(CASE WHEN left(" + text + ",4)='0000' THEN ('0001'||substr(" + text + ",5,15)||'+00 BC')::timestamptz ELSE (left(" + text + ",19)||'Z')::timestamptz END)"
	fraction := "(CASE WHEN substr(" + text + ",20,1)='.' THEN rpad(substr(" + text + ",21,char_length(" + text + ")-21),9,'0')::numeric ELSE 0::numeric END)"
	return "(EXTRACT(EPOCH FROM " + whole + ") * 1000000000::numeric + " + fraction + ")"
}
