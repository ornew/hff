// Package hff defines the values and contracts shared by Human-Friendly Filter
// compilers, local evaluators, and backend translators.
//
// Applications usually configure an engine with a schema and capabilities,
// compile a query into a Program, then pass that Program to a backend Build
// function. Compilation produces a reusable, immutable typed plan; it does not
// construct an evaluator or execute a database query. This separation lets a
// service select local matching, SQL translation, or another backend without
// paying for an unused execution path.
//
// Values represent explicit HFF data. Use Int and Float to retain numeric
// subtypes, Null for an explicit null, and Object/List for owned collections.
// The zero Value is invalid. A missing object key differs from a present key
// containing Null; Lookup's boolean result exposes that distinction. Value
// accessors return an ok flag instead of coercing an incompatible type.
//
// Types describe the input contract. The schema package provides field helpers
// for assembling structs, lists, maps, unions, optional fields, and nullable
// fields. ValidateInput creates an Input whose exact schema can be reused by
// multiple filters; this is separate from compiling a query against that schema.
//
// Program and Node expose the read-only typed IR for backend authors. Programs
// are created by engine compilation, not by manually assembling nodes. An IR
// retains null tests, guards, method resolution, callback bindings, and source
// spans so a backend can preserve HFF semantics or reject unsupported operations.
//
// Errors report configuration, compilation, input, and execution failures.
// Ordinary null results are data and do not become execution errors. Profiles
// describe backend support and guarantees; they do not grant capability policy
// or prove that a caller-owned database satisfies a declared storage contract.
package hff
