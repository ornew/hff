package sql

import (
	"strings"
	"time"

	"github.com/ornew/hff"
)

const (
	minTimestampNanoseconds = "-62167219200000000000"
	maxTimestampNanoseconds = "253402300799999999999"
)

// The full HFF year range fits in PostgreSQL timestamptz. Its Unix epoch
// seconds also fit exactly in binary64, so converting an integral second to
// to_timestamp(double precision) never rounds the seconds component. The
// subsecond nanoseconds remain numeric until formatting or component access.
func (b *builder) timestampUTC(code string) (instant, fraction string) {
	// PostgreSQL numeric division rounds its quotient to a finite scale before
	// floor() sees it. div()/mod() retain the exact integer boundary, including
	// instants before the epoch where div() truncates toward zero.
	quotient := b.project("div((" + code + "), 1000000000::numeric)")
	remainder := b.project("mod((" + code + "), 1000000000::numeric)")
	seconds := b.project("(" + quotient + " - CASE WHEN " + remainder + " < 0 THEN 1 ELSE 0 END)")
	instant = b.project("(to_timestamp(" + seconds + "::double precision) AT TIME ZONE 'UTC')")
	fraction = b.project("((" + code + ") - " + seconds + " * 1000000000::numeric)")
	return instant, fraction
}

func (b *builder) timestampCall(n hff.Node, name string, values []expression) (expression, error) {
	receiver := values[0]
	if n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType {
		return expression{"NULL", ordinaryPresence(values), hff.NullType}, nil
	}
	switch name {
	case "exists":
		return boolean(both(receiver.present, "("+receiver.code+" IS NOT NULL)")), nil
	case "type":
		return expression{"(CASE WHEN " + receiver.code + " IS NULL THEN 'null' ELSE 'timestamp' END)", receiver.present, hff.StringType}, nil
	case "before", "after":
		if len(values) == 2 && values[1].kind == hff.NullType {
			return expression{"NULL::boolean", ordinaryPresence(values), hff.BoolType}, nil
		}
		if len(values) != 2 || values[1].kind != hff.TimestampType {
			return expression{}, b.failure(n, "timestamp comparison requires a recoverable Timestamp argument")
		}
		operator := "<"
		if name == "after" {
			operator = ">"
		}
		code := "(CASE WHEN " + receiver.code + " IS NULL OR " + values[1].code + " IS NULL THEN NULL::boolean ELSE " + receiver.code + operator + values[1].code + " END)"
		return expression{code, ordinaryPresence(values), hff.BoolType}, nil
	case "add":
		if len(values) == 2 && values[1].kind == hff.NullType {
			return expression{"NULL::numeric", ordinaryPresence(values), hff.TimestampType}, nil
		}
		if len(values) != 2 || values[1].kind != hff.DurationType {
			return expression{}, b.failure(n, "timestamp add requires a recoverable Duration argument")
		}
		candidate := b.project("(" + receiver.code + " + " + values[1].code + "::numeric)")
		code := "(CASE WHEN " + candidate + " BETWEEN '" + minTimestampNanoseconds + "'::numeric AND '" + maxTimestampNanoseconds + "'::numeric THEN " + candidate + " ELSE NULL::numeric END)"
		return expression{code, ordinaryPresence(values), hff.TimestampType}, nil
	case "year", "month", "dayofmonth", "dayofyear", "hours", "minutes", "seconds", "milliseconds":
		if len(values) != 1 {
			return expression{}, b.failure(n, "PostgreSQL timestamp components currently require the default UTC zone")
		}
		instant, fraction := b.timestampUTC(receiver.code)
		var component string
		switch name {
		case "year":
			year := "EXTRACT(YEAR FROM " + instant + ")::bigint"
			component = "(CASE WHEN " + year + "<0 THEN " + year + "+1 ELSE " + year + " END)"
		case "month":
			component = "EXTRACT(MONTH FROM " + instant + ")::bigint"
		case "dayofmonth":
			component = "EXTRACT(DAY FROM " + instant + ")::bigint"
		case "dayofyear":
			component = "EXTRACT(DOY FROM " + instant + ")::bigint"
		case "hours":
			component = "EXTRACT(HOUR FROM " + instant + ")::bigint"
		case "minutes":
			component = "EXTRACT(MINUTE FROM " + instant + ")::bigint"
		case "seconds":
			component = "EXTRACT(SECOND FROM " + instant + ")::bigint"
		case "milliseconds":
			component = "floor(" + fraction + "/1000000::numeric)::bigint"
		}
		return expression{component, receiver.present, hff.IntType}, nil
	case "string", "json":
		if len(values) != 1 {
			return expression{}, b.failure(n, "PostgreSQL timestamp formatting currently requires UTC")
		}
		code := b.timestampUTCString(receiver.code)
		if name == "json" {
			code = "to_json(" + code + ")::text"
		}
		return expression{"COALESCE(" + code + ",'null')", receiver.present, hff.StringType}, nil
	case "in":
		return b.timestampIn(n, values)
	}
	return expression{}, b.failure(n, "PostgreSQL does not yet support timestamp method "+name)
}

func (b *builder) timestampUTCString(code string) string {
	instant, fraction := b.timestampUTC(code)
	year := b.project("EXTRACT(YEAR FROM " + instant + ")::bigint")
	astronomicalYear := "(CASE WHEN " + year + "<0 THEN " + year + "+1 ELSE " + year + " END)"
	yearText := "lpad(" + astronomicalYear + "::text,4,'0')"
	fractionText := "(CASE WHEN " + fraction + "=0 THEN '' ELSE '.'||rtrim(lpad(" + fraction + "::bigint::text,9,'0'),'0') END)"
	return "(CASE WHEN " + code + " IS NULL THEN NULL::text ELSE " + yearText + "||to_char(" + instant + ",'-MM-DD\"T\"HH24:MI:SS')||" + fractionText + "||'Z' END)"
}

func (b *builder) timestampIn(n hff.Node, values []expression) (expression, error) {
	receiver := values[0]
	var matches []string
	for index, value := range values[1:] {
		switch value.kind {
		case hff.TimestampType:
			matches = append(matches, b.scalarComparison(receiver, value, hff.Equal).code)
		case hff.NullType:
			matches = append(matches, "FALSE")
		default:
			child, _ := n.Child(index + 1)
			if value.kind == hff.AnyType && acceptsKind(child.Type(), hff.TimestampType) {
				return expression{}, b.failure(child, "timestamp membership cannot recover a dynamic Timestamp argument")
			}
			matches = append(matches, "FALSE")
		}
	}
	code := "(CASE WHEN " + receiver.code + " IS NULL THEN NULL::boolean ELSE (" + strings.Join(matches, " OR ") + ") END)"
	return expression{code, membershipPresence(values), hff.BoolType}, nil
}

// PostgreSQL input validation cannot by itself enforce HFF's exact RFC 3339
// grammar or nanosecond precision. For now SQL builds only literal conversion
// calls and binds the exact resulting instant; data-dependent parsing is
// rejected rather than silently rounding it to microseconds.
func (b *builder) timestampLiteralCall(n hff.Node) (expression, error) {
	if n.ChildCount() != 1 {
		return expression{}, b.failure(n, "timestamp() expects a string receiver")
	}
	receiver, _ := n.Child(0)
	if n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType {
		value, err := b.expression(receiver)
		if err != nil {
			return expression{}, err
		}
		return expression{"NULL", value.present, hff.NullType}, nil
	}
	if receiver.Kind() != hff.LiteralNode {
		return expression{}, b.failure(n, "PostgreSQL timestamp() currently requires a literal RFC 3339 string")
	}
	value, _ := receiver.Literal()
	if value.Kind() == hff.NullKind {
		return expression{"NULL::numeric", "TRUE", hff.TimestampType}, nil
	}
	text, ok := value.AsString()
	if !ok {
		return expression{}, b.failure(n, "timestamp() requires a string")
	}
	stamp, ok := parseTimestampLiteral(text)
	if !ok {
		return expression{"NULL::numeric", "TRUE", hff.TimestampType}, nil
	}
	return b.parameter(n, hff.Timestamp(stamp))
}

func parseTimestampLiteral(text string) (time.Time, bool) {
	if len(text) < 20 || text[4] != '-' || text[7] != '-' || text[10] != 'T' || text[13] != ':' || text[16] != ':' {
		return time.Time{}, false
	}
	for _, index := range []int{0, 1, 2, 3, 5, 6, 8, 9, 11, 12, 14, 15, 17, 18} {
		if text[index] < '0' || text[index] > '9' {
			return time.Time{}, false
		}
	}
	if text[17:19] > "59" {
		return time.Time{}, false
	}
	index := 19
	if text[index] == '.' {
		index++
		start := index
		for index < len(text) && text[index] >= '0' && text[index] <= '9' {
			index++
		}
		if index-start < 1 || index-start > 9 {
			return time.Time{}, false
		}
	}
	zone := text[index:]
	if zone != "Z" {
		if len(zone) != 6 || (zone[0] != '+' && zone[0] != '-') || zone[3] != ':' {
			return time.Time{}, false
		}
		for _, i := range []int{1, 2, 4, 5} {
			if zone[i] < '0' || zone[i] > '9' {
				return time.Time{}, false
			}
		}
		if zone[1:3] > "23" || zone[4:6] > "59" {
			return time.Time{}, false
		}
	}
	stamp, err := time.ParseInLocation(time.RFC3339Nano, text, time.UTC)
	if err != nil || stamp.UTC().Year() < 0 || stamp.UTC().Year() > 9999 {
		return time.Time{}, false
	}
	return stamp, true
}
