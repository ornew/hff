package sql

import (
	"strings"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
)

// Profile describes statically known translation support for a dialect without
// connecting to a database. Runtime database, collation, Unicode, regex, and
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
		Backend: "sql/postgresql",
		Details: []hff.ProfileDetail{
			{Name: "execution", Value: "caller owns database execution, input/schema validity, and database timeout configuration"},
			{Name: "limits", Value: "translation cannot enforce HFF step, generated-value, or JSON-depth budgets in the database"},
			{Name: "evaluation.order", Value: "SQL preserves supported expression results; database planning may reorder work and does not guarantee HFF evaluation steps"},
			{Name: "bindings", Value: "explicit scalar, JSON/JSONB, composite, flattened struct, or relation bindings; version-1 JSONB numeric metadata supplements schema; caller guarantees stored metadata consistency; unrecoverable representations are rejected"},
			{Name: "json.parsing", Value: "fromjson requires PostgreSQL 16+ JSON validation; caller guarantees decoded JSON strings and keys contain no NUL"},
			{Name: "regex", Value: "RE2-compatible regex translation is not supported by this implementation"},
			{Name: "extensions.contract", Value: "translators use typed expressions and parameter binding; caller guarantees trusted SQL templates and database function behavior"},
		},
	}
	for _, id := range capabilities.Standard().IDs() {
		feature := hff.FeatureSupport{ID: id, Level: hff.Conditional,
			Conditions: "Build checks supported types, operator semantics, and explicit schema-to-column bindings"}
		if id == capabilities.RegexOperators {
			feature.Level, feature.Conditions = hff.Unsupported, "RE2-compatible translation is unavailable"
		}
		if name, method := strings.CutPrefix(string(id), "method:"); method && !profileSQLMethod(name) {
			feature.Level, feature.Conditions = hff.Unsupported, "method translation is unavailable"
		}
		if id == capabilities.Method("string.fromjson") {
			feature.Conditions += "; PostgreSQL 16+; decoded strings and keys must contain no NUL"
		}
		config.Features = append(config.Features, feature)
	}
	return config, nil
}

// Keep this list at signature granularity rather than inferring support from
// method names shared by unrelated receiver families.
func profileSQLMethod(id string) bool {
	switch id {
	case "any.type", "any.json", "any.string", "any.in", "any.exists",
		"bool.and", "bool.or", "bool.not", "string.fromjson", "string.len", "string.contains", "string.startswith", "string.endswith", "string.concat",
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
