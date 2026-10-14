package sql

import (
	"runtime"
	"strings"
	"unicode"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
)

// Profile describes statically known translation support for a dialect without
// connecting to a database. Unicode casing and regex parsing report their Go
// implementation versions; database, collation, database regex engine, and
// time-zone versions remain unknown.
func Profile(dialect Dialect) (hff.Profile, error) {
	config, err := sqlProfileConfig(dialect)
	if err != nil {
		return hff.Profile{}, err
	}
	return hff.NewProfile(config)
}

// Profile returns the built query's translation contract, including its
// compile policy, limits, and registered translations. It does not inspect the
// executing database or turn caller-owned storage guarantees into HFF checks.
func (q *Query) Profile() hff.Profile {
	if q == nil {
		return hff.Profile{}
	}
	return q.profile
}

func sqlProfileConfig(dialect Dialect) (hff.ProfileConfig, error) {
	if dialect != PostgreSQL {
		return hff.ProfileConfig{}, configuration("explicit supported SQL dialect required")
	}
	config := hff.ProfileConfig{
		Backend:  "sql/postgresql",
		Versions: []hff.CompatibilityVersion{{Component: "unicode", Version: unicode.Version}, {Component: "regex.parser", Version: "Go regexp " + runtime.Version()}},
		Details: []hff.ProfileDetail{
			{Name: "float.requirements", Value: "PostgreSQL 16+ Float input validation and extra_float_digits>0 for round-trip-safe Float transport; caller configures the database session"},
			{Name: "float.model", Value: "PostgreSQL approximate double precision; Float results and text need not match other backends at rounding boundaries"},
			{Name: "float.parsing", Value: "PostgreSQL 16+ pg_input_is_valid and double-precision input; rejected overflow or underflow becomes null"},
			{Name: "float.arithmetic", Value: "native Float arithmetic for safe ordinary magnitudes, cancellation and zero; extreme magnitudes use PostgreSQL numeric arithmetic from native Float text with validated Float output; fallback rounding and zero sign may differ"},
			{Name: "float.rounding", Value: "native PostgreSQL round, ceil and floor; Float round uses the backend midpoint rule"},
			{Name: "float.aggregation", Value: "schema-known Float sum converts each finite value through round-trip Float text to numeric (requires extra_float_digits>0), accumulates with PostgreSQL SUM(numeric), then validates and converts the final result to double precision; intermediate overflow, result rounding, and zero sign can differ from local list-order addition"},
			{Name: "float.format", Value: "native PostgreSQL shortest-precise Float text with extra_float_digits>0; JSON retains a decimal/exponent Float marker and signed zero is preserved"},
			{Name: "execution", Value: "caller owns database execution, input/schema validity, and database timeout configuration"},
			{Name: "limits", Value: "translation cannot enforce HFF step, generated-value, or JSON-depth budgets in the database"},
			{Name: "evaluation.order", Value: "SQL preserves supported expression results; database planning may reorder work and does not guarantee HFF evaluation steps"},
			{Name: "bindings", Value: "explicit scalar, JSON/JSONB, composite, flattened struct, or relation bindings; version-1 JSONB numeric metadata supplements schema; caller guarantees stored metadata consistency; unrecoverable representations are rejected"},
			{Name: "unicode.casing", Value: "locale-independent simple Unicode mappings are bound by the translator; database lower/upper rules are not used"},
			{Name: "number.parsing", Value: "number() requires PostgreSQL 16+ input validation; JSON number spelling, int64 bounds, and finite binary64 results are checked"},
			{Name: "json.parsing", Value: "fromjson requires PostgreSQL 16+ JSON validation; caller guarantees decoded JSON strings and keys contain no NUL"},
			{Name: "duration", Value: "string parsing, computed durations, native bigint nanosecond duration columns, and schema-known JSON/JSONB duration strings are supported; ambiguous JSON unions and composite duration materialization are unavailable"},
			{Name: "timestamp", Value: "schema-known timestamptz bindings use optional SMALLINT 0–999 nanosecond remainders; schema-known JSON/JSONB Timestamps use canonical UTC RFC 3339 strings and exact numeric epoch nanoseconds; the caller guarantees storage validity and years 0000–9999; literal RFC 3339 parsing and UTC methods are supported"},
			{Name: "bytes", Value: "native bytea, computed bytes, and schema-known JSON/JSONB Bytes use strict standard base64; base64r/frombase64r use strict unpadded standard base64; ambiguous Bytes representations are rejected"},
			{Name: "regex", Value: "literal non-NUL ASCII RE2 subset translated to PostgreSQL 15+ ARE; dynamic patterns, case folding, line anchors, Unicode classes, dot and quantified anchors are unavailable"},
			{Name: "extensions.contract", Value: "translators use typed expressions and parameter binding; caller guarantees trusted SQL templates and database function behavior"},
		},
	}
	for _, id := range capabilities.Standard().IDs() {
		feature := hff.FeatureSupport{ID: id, Level: hff.Conditional,
			Conditions: "Build checks supported types, operator semantics, and explicit schema-to-column bindings; Float processing requires PostgreSQL 16+ and extra_float_digits>0"}
		if id == capabilities.RegexOperators || id == capabilities.Method("string.matches") {
			feature.Conditions += "; PostgreSQL 15+; literal non-NUL ASCII RE2 subset only"
		}
		if name, method := strings.CutPrefix(string(id), "method:"); method && !profileSQLMethod(name) {
			feature.Level, feature.Conditions = hff.Unsupported, "method translation is unavailable"
		}
		if id == capabilities.Method("string.fromjson") {
			feature.Conditions += "; PostgreSQL 16+; decoded strings and keys must contain no NUL"
		}
		if id == capabilities.Method("string.number") {
			feature.Conditions += "; PostgreSQL 16+ input validation"
		}
		config.Features = append(config.Features, feature)
	}
	return config, nil
}

// Keep this list at signature granularity rather than inferring support from
// method names shared by unrelated receiver families.
func profileSQLMethod(id string) bool {
	switch id {
	case "string.duration", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds", "string.timestamp", "timestamp.year", "timestamp.month", "timestamp.dayofmonth", "timestamp.dayofyear", "timestamp.hours", "timestamp.minutes", "timestamp.seconds", "timestamp.milliseconds", "timestamp.before", "timestamp.after", "timestamp.add", "any.type", "any.json", "any.string", "any.in", "any.exists",
		"bytes.len", "bytes.base64", "bytes.base64r", "string.frombase64", "string.frombase64r", "number.round", "number.ceil", "number.floor", "bool.and", "bool.or", "bool.not", "string.bool", "string.number", "string.fromjson", "string.matches", "string.lower", "string.upper", "string.len", "string.contains", "string.startswith", "string.endswith", "string.concat",
		"list.size", "list.count", "list.filter", "list.sum", "list.has", "list.contains", "list.unique", "list.sort", "list.concat", "list.any", "list.all", "list.map", "list.flatmap", "list.foldl", "list.foldr",
		"object.size", "object.has", "object.get", "object.keys", "object.values", "object.items":
		return true
	}
	return false
}

func queryProfile(dialect Dialect, program hff.Program, methods map[string]MethodImplementation) (hff.Profile, error) {
	config, err := sqlProfileConfig(dialect)
	if err != nil {
		return hff.Profile{}, err
	}
	config.Capabilities = program.Capabilities()
	configured := program.Limits()
	compile := hff.Limits{MaxQueryBytes: configured.MaxQueryBytes, MaxSyntaxDepth: configured.MaxSyntaxDepth,
		MaxCallbackDepth: configured.MaxCallbackDepth, MaxCompileSteps: configured.MaxCompileSteps}
	config.CompileLimits = &compile
	for id, method := range methods {
		config.Methods = append(config.Methods, method.declaration)
		config.Features = append(config.Features, hff.FeatureSupport{ID: capabilities.Method(id), Level: hff.Conditional,
			Conditions: "registered translator must support the resolved types and its trusted SQL contract"})
	}
	return hff.NewProfile(config)
}
