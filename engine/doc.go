// Package engine configures HFF compilation and validates host records.
//
// New requires an input schema and an explicitly selected capability policy,
// such as capabilities.Standard(). The root schema is a nonnullable struct or
// string-keyed map. Options apply from left to right, allowing a common
// WithConfig configuration to be followed by service-specific overrides.
// Configuration is validated and copied before New returns.
//
// Compile checks the fixed query grammar, declared types, feature permissions,
// and compile limits. Its Program is independent of an execution backend.
// Pass it to local.Build for record matching or sql.Build for query translation;
// neither backend is imported or constructed by engine compilation.
//
// Configure an Engine once at service startup and reuse it concurrently. Compile
// a filter once when accepting a query, then reuse the built backend artifact
// for multiple records or database executions. Avoid recompiling the same query
// for each record.
//
// Validate checks an actual record, including required fields and numeric kinds,
// and returns an immutable hff.Input. Local Match validates ordinary values on
// each call; MatchInput can reuse a previously validated input when the Program's
// schema is exactly equal. Validate does not determine whether a record matches
// a query, and Compile does not validate any runtime record.
//
// Compile may return multiple source-located diagnostics. Use errors.As with
// *hff.Diagnostics or *hff.Error rather than parsing error message text.
package engine
