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
			{Name: "execution", Value: "caller owns database execution, input/schema validity, and database timeout configuration"},
			{Name: "limits", Value: "translation cannot enforce HFF step, generated-value, or JSON-depth budgets in the database"},
			{Name: "evaluation.order", Value: "SQL preserves supported expression results; database planning may reorder work and does not guarantee HFF evaluation steps"},
			{Name: "bindings", Value: "explicit scalar, JSON/JSONB, composite, flattened struct, or relation bindings; version-1 JSONB numeric metadata supplements schema; caller guarantees stored metadata consistency; unrecoverable representations are rejected"},
			{Name: "unicode.casing", Value: "locale-independent simple Unicode mappings are bound by the translator; database lower/upper rules are not used"},
			{Name: "number.parsing", Value: "number() requires PostgreSQL 16+ input validation; JSON number spelling, int64 bounds, and finite binary64 results are checked"},
			{Name: "json.parsing", Value: "fromjson requires PostgreSQL 16+ JSON validation; caller guarantees decoded JSON strings and keys contain no NUL"},
			{Name: "duration", Value: "string parsing and computed integral-nanosecond durations are supported; native and JSON duration storage bindings are unavailable"},
			{Name: "bytes", Value: "native bytea and computed bytes are supported; JSON collection storage of bytes is unavailable"},
			{Name: "regex", Value: "literal non-NUL ASCII RE2 subset translated to PostgreSQL 15+ ARE; dynamic patterns, case folding, line anchors, Unicode classes, dot and quantified anchors are unavailable"},
			{Name: "extensions.contract", Value: "translators use typed expressions and parameter binding; caller guarantees trusted SQL templates and database function behavior"},
		},
	}
	for _, id := range capabilities.Standard().IDs() {
		feature := hff.FeatureSupport{ID: id, Level: hff.Conditional,
			Conditions: "Build checks supported types, operator semantics, and explicit schema-to-column bindings"}
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
	case "string.duration", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds", "any.type", "any.json", "any.string", "any.in", "any.exists",
		"bytes.len", "bytes.base64", "string.frombase64", "number.round", "number.ceil", "number.floor", "bool.and", "bool.or", "bool.not", "string.bool", "string.number", "string.fromjson", "string.matches", "string.lower", "string.upper", "string.len", "string.contains", "string.startswith", "string.endswith", "string.concat",
		"list.size", "list.has", "list.contains", "list.unique", "list.sort", "list.concat", "list.any", "list.all", "list.map", "list.flatmap", "list.foldl", "list.foldr",
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
