package sql

import (
	"fmt"
	"strings"

	"github.com/ornew/hff"
)

// Computed durations use integral nanoseconds, without defining a storage
// binding. Keep this separate from scalarKind, which accepts native columns.
func computedScalarKind(typ hff.Type) (hff.TypeKind, bool) {
	if kind, ok := scalarKind(typ); ok {
		return kind, true
	}
	if typ.Kind() == hff.DurationType {
		return hff.DurationType, true
	}
	if typ.Kind() == hff.TimestampType {
		return hff.TimestampType, true
	}
	if typ.Kind() == hff.UnionType {
		kind := hff.NullType
		for _, member := range typ.Members() {
			if member.Kind() == hff.NullType {
				continue
			}
			k, ok := computedScalarKind(member)
			if !ok || (kind != hff.NullType && kind != k) {
				return hff.InvalidType, false
			}
			kind = k
		}
		return kind, true
	}
	return hff.InvalidType, false
}

func (b *builder) durationCall(n hff.Node, name string, receiver expression) (expression, error) {
	if n.Type().Kind() == hff.NullType || n.Type().Kind() == hff.NeverType || receiver.kind == hff.NullType {
		return expression{"NULL", receiver.present, hff.NullType}, nil
	}
	if name == "duration" {
		text, err := b.stringConversionInput(n, receiver)
		if err != nil {
			return expression{}, err
		}
		return expression{b.parseDuration(text), receiver.present, hff.DurationType}, nil
	}
	if receiver.kind == hff.AnyType {
		receiverNode, _ := n.Child(0)
		if !jsonRootHasKind(b.jsonStoredType(receiverNode), hff.DurationType) {
			return expression{}, b.failure(n, "duration units require a recoverable Duration receiver")
		}
		format, ok := b.jsonValues[receiver.code]
		if !ok {
			return expression{}, b.failure(n, "duration units require a JSON-backed Duration receiver")
		}
		class := string(format) + "_typeof(" + receiver.code + ")"
		text := "(CASE WHEN " + class + "='string' THEN " + receiver.code + " #>> '{}'::text[] ELSE NULL::text END)"
		receiver = expression{b.parseDuration(text), receiver.present, hff.DurationType}
	}
	if receiver.kind != hff.DurationType {
		return expression{}, b.failure(n, "duration units require a duration value")
	}
	if name == "nanoseconds" {
		return expression{receiver.code, receiver.present, hff.IntType}, nil
	}
	unit := map[string]int64{"hours": 3600000000000, "minutes": 60000000000,
		"seconds": 1000000000, "milliseconds": 1000000, "microseconds": 1000}[name]
	if unit == 0 {
		return expression{}, b.failure(n, "unsupported duration unit")
	}
	// Match the local kernel's two rounding steps, rather than dividing exact
	// decimal nanoseconds before a single conversion to binary64.
	return expression{fmt.Sprintf("(%s::double precision / %d::double precision)", receiver.code, unit), receiver.present, hff.FloatType}, nil
}

func (b *builder) parseDuration(text string) string {
	return b.parseDurationWithProject(text, b.project)
}

// JSON array elements and object fields use aliases local to a nested SQL
// expression. Their conversions must stay in that scope rather than adding
// projections to the enclosing builder.
func (b *builder) parseDurationInline(text string) string {
	return b.parseDurationWithProject(text, func(code string) string { return code })
}

func (b *builder) parseDurationWithProject(text string, project func(string) string) string {
	raw := project("(" + text + " COLLATE \"C\")")
	const amount = `([0-9]+(\.[0-9]*)?|\.[0-9]+)`
	const component = amount + `(ms|us|ns|h|m|s)`
	// Explicit escape literals keep these fixed patterns independent of the
	// connection's standard_conforming_strings setting.
	pattern := strings.ReplaceAll(`\A[+-]?(0|(`+component+`)+)\Z`, `\`, `\\`)
	safe := project("(CASE WHEN " + raw + " ~ E'" + pattern + "' THEN " + raw + " ELSE NULL::text END)")
	// Validate the entire input before component casts can execute.
	parts, columns, whole, result := b.nextAlias(), b.nextAlias(), b.nextAlias(), b.nextAlias()
	code := `WITH RECURSIVE @parts AS MATERIALIZED (
SELECT ltrim(split_part(m[1],'.',1),'0') AS integral,
       convert_to(rtrim(split_part(m[1],'.',2),'0'),'UTF8') AS fraction,
       CASE m[3] WHEN 'h' THEN 3600000000000::numeric WHEN 'm' THEN 60000000000::numeric
       WHEN 's' THEN 1000000000::numeric WHEN 'ms' THEN 1000000::numeric
       WHEN 'us' THEN 1000::numeric ELSE 1::numeric END AS unit
FROM regexp_matches(@safe,E'@component','g') AS matches(m)
), @columns(position,carry,exact) AS (
SELECT COALESCE(max(octet_length(fraction)),0),0::numeric,TRUE FROM @parts
UNION ALL
SELECT position-1,div(amount,10),exact AND mod(amount,10)=0
FROM @columns CROSS JOIN LATERAL (
 SELECT carry+COALESCE(sum((CASE WHEN position>0 AND octet_length(fraction)>=position
 THEN (get_byte(fraction,position-1)-48)::numeric ELSE 0::numeric END)*unit),0::numeric) AS amount
 FROM @parts
) AS contribution WHERE position>0
), @whole AS (
SELECT COALESCE(sum((CASE WHEN length(integral)<=19
 THEN COALESCE(NULLIF(integral,''),'0')::numeric ELSE 0::numeric END)*unit),0::numeric) AS value,
 COALESCE(bool_or(length(integral)>19),FALSE) AS overflow FROM @parts
), @result AS (
SELECT CASE WHEN left(@raw,1)='-' THEN -(value+carry) ELSE value+carry END AS ns,exact,overflow
FROM @columns CROSS JOIN @whole WHERE position=0
)
SELECT CASE WHEN @safe IS NULL OR NOT exact OR overflow
 OR ns NOT BETWEEN '-9223372036854775808'::numeric AND '9223372036854775807'::numeric
THEN NULL::bigint ELSE ns::bigint END FROM @result`
	// Validation restricts fractional digits to ASCII, so byte offsets preserve
	// their positions without repeatedly scanning UTF-8 text. Both byte bounds
	// are guarded even if the planner evaluates a terminating recursive row.
	// All recursive arithmetic is integral numeric. Carrying decimal digits
	// avoids PostgreSQL numeric's finite fractional-scale limit while summing
	// fractional components exactly before checking nanosecond integrality.
	return "(" + strings.NewReplacer("@parts", parts, "@columns", columns, "@whole", whole,
		"@result", result, "@safe", safe, "@raw", raw, "@component", strings.ReplaceAll(component, `\`, `\\`)).Replace(code) + ")"
}

func (b *builder) durationText(code string) string {
	return b.durationTextWithProject(code, b.project)
}

func (b *builder) durationTextInline(code string) string {
	return b.durationTextWithProject(code, func(value string) string { return value })
}

func (b *builder) durationTextWithProject(code string, project func(string) string) string {
	n := project("abs(" + code + "::numeric)")
	h := project("div(" + n + ",3600000000000)")
	m := project("div(mod(" + n + ",3600000000000),60000000000)")
	s := project("div(mod(" + n + ",60000000000),1000000000)")
	f := project("mod(" + n + ",1000000000)")
	return "(CASE WHEN " + code + " IS NULL THEN NULL::text ELSE " +
		"(CASE WHEN " + code + "<0 THEN '-' ELSE '' END)||" +
		"(CASE WHEN " + h + ">0 THEN " + h + "::text||'h' ELSE '' END)||" +
		"(CASE WHEN " + m + ">0 THEN " + m + "::text||'m' ELSE '' END)||" +
		"(CASE WHEN mod(" + n + ",60000000000)>0 OR (" + h + "=0 AND " + m + "=0) THEN " + s + "::text||" +
		"(CASE WHEN " + f + ">0 THEN '.'||rtrim(lpad(" + f + "::text,9,'0'),'0') ELSE '' END)||'s' ELSE '' END) END)"
}

// Duration membership compares typed computed values without materializing
// them as JSON numbers. A null needle is data; no-value still propagates.
func (b *builder) durationIn(n hff.Node, values []expression) (expression, error) {
	receiver := values[0]
	var matches []string
	for index, value := range values[1:] {
		switch value.kind {
		case hff.NullType:
			matches = append(matches, "FALSE")
		case hff.DurationType:
			matches = append(matches, b.scalarComparison(receiver, value, hff.Equal).code)
		default:
			child, _ := n.Child(index + 1)
			if value.kind == hff.AnyType && acceptsKind(child.Type(), hff.DurationType) {
				if !jsonRootHasKind(b.jsonStoredType(child), hff.DurationType) {
					return expression{}, b.failure(child, "duration membership cannot recover a dynamic duration argument")
				}
				format, ok := b.jsonValues[value.code]
				if !ok {
					return expression{}, b.failure(child, "duration membership requires a JSON-backed argument")
				}
				class := string(format) + "_typeof(" + value.code + ")"
				text := "(" + value.code + " #>> '{}'::text[])"
				candidate := expression{b.parseDurationInline(text), value.present, hff.DurationType}
				comparison := b.scalarComparison(receiver, candidate, hff.Equal)
				matches = append(matches, "(CASE WHEN "+class+"='string' THEN "+comparison.code+" ELSE FALSE END)")
				continue
			}
			// A statically disjoint family cannot match a duration. Its
			// no-value flag still contributes to membershipPresence.
			matches = append(matches, "FALSE")
		}
	}
	code := "(CASE WHEN " + receiver.code + " IS NULL THEN NULL::boolean ELSE (" + strings.Join(matches, " OR ") + ") END)"
	return expression{code, membershipPresence(values), hff.BoolType}, nil
}
