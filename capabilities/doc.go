// Package capabilities defines which HFF features a service permits at compile
// time. It controls semantics, not the grammar: every engine parses the same
// syntax and rejects a disabled operation during Compile.
//
// Start with Standard for all built-in features, or None for an explicitly empty
// policy. Derive a policy with Allow and Without; these return new immutable
// sets rather than changing their receiver. The zero Set means unspecified and
// is rejected by engine.New.
//
// Operator IDs control operations such as Arithmetic and RegexOperators.
// Method("string.contains") and Method("list.contains") are separate permissions
// even though both use contains in a query. Group IDs, such as Regex and
// StructuralEquality, select related IDs together. IDs returns concrete enabled
// IDs in sorted order, not the group aliases used to construct the set.
//
// Dependencies are checked rather than silently enabled. For example, allowing
// list.any also requires Callbacks; allowing NestedCallbacks requires Callbacks.
// Check validates a built-in policy. CheckWithMethods also recognizes declared
// extension IDs; engine.New performs the corresponding check with its methods.
//
// Permission does not imply backend implementation support. Compile checks the
// service policy; a later backend Build may still reject an operation it cannot
// translate faithfully. Use backend profiles to inspect support separately.
package capabilities
