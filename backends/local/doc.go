// Package local builds an in-process evaluator for an already compiled HFF
// Program. It evaluates records without database access or other I/O.
//
// The usual workflow is engine.New, Engine.Compile, local.Build, then
// Filter.Match. Build checks the Program and backend configuration once and
// returns an immutable Filter that can match many records concurrently. Match
// accepts an explicit hff.Value object and validates it against the Program's
// schema before evaluating the filter.
//
// Match returns (false, nil) for a completed non-match. Ordinary conversion or
// arithmetic failures inside a query become null according to HFF semantics;
// invalid host input, cancellation, and exhausted budgets return typed errors.
// Do not treat an error as a completed false result. MatchInput skips repeated
// input validation for an hff.Input with an exactly matching schema contract.
//
// WithLimits configures input and runtime budgets; zero fields use resolved
// defaults and WithUnlimitedLimits disables those budgets explicitly. Compile
// limits belong to the Program and cannot be changed by local Build options.
// Named time zones require caller-supplied versioned TZif data through
// WithTimeZones; UTC and fixed offsets are always available.
//
// Extensions are declared to the engine separately from local implementations.
// Pair a declaration with Implement and register it using WithMethods. The
// implementation receives a Call for cancellation and extra budget charging;
// it must be deterministic, side-effect-free, and safe for concurrent matches.
package local
