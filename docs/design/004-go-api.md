# 004: Go API and Backend Construction

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

Separate service configuration, language compilation, and backend construction.
An immutable engine compiles a query into a backend-independent program. A local
builder turns that program into a filter; another builder can translate it into
a backend query without constructing a local evaluator.

This record defines the API contract. Availability is documented separately as
the packages are implemented.
The [language specification](../../spec/README.md) defines their semantic contract.

The common compiler and local backend's core operators, values, indexing,
guards, short-circuit behavior, per-match budgets, scalar/string method
families, list/object methods, collection callbacks, base64 and JSON conversions,
temporal methods, and local extension evaluators are implemented. The SQL
package builds parameterized PostgreSQL predicates for a scalar subset and
supports separately registered typed translators for supported scalar
extensions; broader SQL semantics remain in progress.

## Motivation

A service chooses a schema and supported features, then accepts many queries.
Each query can be evaluated repeatedly or translated to a processing system.
Building a local evaluator during common compilation wastes work for a service
that only needs SQL. A shared typed program also prevents each backend from
reimplementing name resolution, type checking, and language restrictions.

## Goals

- Configure a service once and compile multiple queries against that configuration.
- Reuse compiled programs without mutating configuration or query state.
- Build only the selected backend artifact.
- Keep schemas and values independent of Go reflection and SQL drivers.
- Preserve input validation, null/no-value semantics, and explicit cancellation.
- Allow Go-value adapters and external backend implementations.

## Non-goals

Database connections, query execution, and connection pooling belong to the
calling service. The HFF runtime does not perform I/O. Parser implementation
nodes are not a substitute for a validated public program representation.

## Design

### Configuration and compilation

The package layout separates configuration from backend construction:

```text
hff/                 shared values and validated program contracts
    engine/          service configuration and common compilation
    schema/          schema and field constructors
    capabilities/    feature selection and standard profiles
    backends/
        local/       local evaluation
        sql/         SQL translation
```

The root package contains shared contracts, rather than forwarding engine or
backend constructors. Neither the engine nor the common representation imports
backend packages. A backend can depend on the validated program without making
common compilation depend on that backend. External backend packages need not
live in this repository.

The constructor accepts functional options:

```go
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.String("name"),
        schema.Int("age"),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `name="Alice" age>=18`)
if err != nil {
    return err
}
```

Configuration can also be assembled as data:

```go
config := &engine.Config{
    Schema: schema.Struct(schema.String("name"), schema.Int("age")),
    Capabilities: capabilities.Standard(),
}
e, err := engine.New(engine.WithConfig(config))
```

Both entry points use the same configuration validation and snapshot semantics.
Options apply from left to right; a later assignment replaces an earlier value
for the same setting. WithConfig supplies a complete configuration at its position
in that sequence, including zero values. Individual options after it can override
selected settings. The resulting configuration is validated once, then copied
into the immutable engine. WithConfig does not retain a mutable configuration
pointer in the constructed engine.

```go
e, err := engine.New(
    engine.WithConfig(base),
    engine.WithSchema(serviceSchema),
)
```

WithConfig takes a package-specific Config pointer. A nil pointer is a
configuration error rather than a panic or an implicit default. The same option
ordering, validation, and snapshot rules apply to backend builders.

New validates and snapshots configuration. Compile parses the complete source,
resolves its schema, checks types, validates field dependence, and checks enabled
language capabilities. It produces typed operations, source spans, and result
annotations. It does not construct an evaluation closure, VM, SQL expression,
or backend-specific query. Backend support is checked by Build, which is the
backend-specific stage of compilation.

Engine and Program are immutable and support concurrent use. Per-compilation
state, cancellation, and budgets are call-local. Neither object silently changes
its schema or capabilities after construction. Capabilities must be selected
explicitly, including when the service wants the complete standard profile.

### Backend-specific construction

```go
filter, err := local.Build(ctx, program)
if err != nil {
    return err
}
```

Build accepts backend-specific functional options, including WithConfig for
settings assembled as a configuration value. For example:

```go
filter, err := local.Build(ctx, program, local.WithConfig(localConfig))
```

A builder validates its backend's support and creates only its own artifact.
The same program can be built for multiple targets. A local filter is reusable;
per-record evaluation state is separate from the filter. Backend-specific
optimizations must preserve the program's validated semantics.

The local backend accepts an optional versioned time-zone profile at Build:

```go
profile := &local.TimeZoneProfile{
    Version: "tzdata-2026a",
    Zones:   map[string][]byte{"America/New_York": newYorkTZif},
    Aliases: map[string]string{"US/Eastern": "America/New_York"},
}
filter, err := local.Build(ctx, program, local.WithTimeZones(profile))
```

Build copies and parses TZif bytes and aliases into the immutable filter; later
matching performs no filesystem or environment lookups. Aliases resolve
directly to a profile zone or UTC, without chains. UTC and fixed offsets are
built in and cannot be overridden. Invalid profiles are configuration errors.
`Filter.TimeZoneVersion()` reports the profile version, or an empty string when
only built-in zones are available.

```go
matched, err := filter.Match(ctx, record)
if err != nil {
    return err
}
```

Match accepts an explicit HFF Value representing the input record and validates
it against the program's schema on each call. Its result is `(bool, error)`:
false is a completed non-match, whereas an input validation error, resource-limit
error, or context cancellation indicates that evaluation did not complete.
Ordinary null and no-value follow language boolean conversion without an error.
### Validated input reuse

```go
input, err := e.Validate(ctx, record)
if err != nil {
    return err
}
matched, err := filter.MatchInput(ctx, input)
```

Validate validates the whole record against the engine's input contract and
returns an immutable `hff.Input`. Input retains the validated value and its
schema contract; hosts cannot manufacture a validated Input from an unchecked
Value. MatchInput checks contract identity before evaluating, without repeating
record validation. Input is reusable across programs and engines with exactly
the same input contract, regardless of their capability selection.

The contract includes root and field types, numeric subtypes, required/optional
presence, nullability, and missing-value handling. Contract equality is structural
and independent of constructor object identity. Field declaration order and
union member order do not change the contract. A digest may accelerate comparison,
but hash equality alone is not proof of compatibility. Neither schema widening
nor inferred partial compatibility is performed. An incompatible or invalid
Input produces an input error and no completed match result.

Validate accepts context cancellation and configured validation limits. The
validated value must own immutable storage, so caller mutations cannot invalidate
validation. Match validates the record once for that call and uses the same
evaluation path as MatchInput after validation. All three operations preserve
the distinction between input rejection, evaluation cancellation, and a completed
false result.

### Public typed representation

Program exposes a read-only typed intermediate representation for external
backend builders. HFF alone constructs and validates this representation. Node
is a concrete opaque value, not an interface that hosts can implement. Its
accessors expose immutable handles, scalar copies, or defensive copies; they do
not expose mutable compiler storage.

The inspection surface is:

```go
func (p Program) Valid() bool
func (p Program) Root() Node
func (p Program) Source() string
func (p Program) Schema() Type
func (p Program) Capabilities() capabilities.Set
func (p Program) Limits() Limits
func (p Program) UnlimitedLimits() bool
func (n Node) Valid() bool
func (n Node) Kind() NodeKind
func (n Node) Type() Type
func (n Node) Span() Span
func (n Node) MayNoValue() bool
func (n Node) ChildCount() int
func (n Node) Child(i int) (Node, bool)
func (n Node) Literal() (Value, bool)
func (n Node) Path() (Path, bool)
func (n Node) Field() (string, bool)
func (n Node) Operator() (Operator, bool)
func (n Node) Constraint() (Type, bool)
func (n Node) Method() (Method, bool)
func (n Node) Binding() (Binding, bool)
func (n Node) Parameters() ([]Binding, bool)
func (n Node) Captures() ([]Binding, bool)
func (n Node) Keys() ([]string, bool)
```

These types belong to the root `hff` package. Payload accessors return false for
an inapplicable node kind; Child returns false for an invalid index. Path returns
a copy. Span describes the half-open UTF-8 byte range in the original source.
Type, Method, Operator, and Binding are immutable descriptors. A call's Method
contains only signatures statically applicable to that call, not the entire
method registry. An Any or union receiver can have multiple family signatures
for runtime dispatch, and every statically applicable signature must be enabled
or Compile reports a capability error. A TypeGuardNode narrows the receiver
before method resolution, so a guarded call checks only signatures applicable to
the narrowed type. A runtime value incompatible with all applicable signatures
yields null. A null-only receiver has no non-null family to select a signature;
all known applicable candidates must be enabled before null propagates. Each
signature has a stable ID, receiver/parameter/result types, and variadic flag.
Backends use those IDs without repeating common type resolution and preserve any
required dynamic checks. `Constraint()` returns the exact user-specified type
guard constraint, not the inferred node result type, and false for other node
kinds. The node's `Type()` reports the constraint intersected with its known
receiver type; runtime checking uses the original constraint.

The public kinds are Invalid, MatchAll, Literal, Variable, Parameter, Field,
Index, Guard, NullTest, Binary, Negate, Bool, And, Or, Not, List, Object, Call,
Callback, and TypeGuard. TypeGuard is appended after the existing kinds to
preserve their numeric values. Its child is the guarded expression and its
payload is available through `Constraint()`. Their child order and payload
mapping are documented in the
[Typed IR Reference](../reference/program-ir.md). Binding IDs are unique within one
Program, including nested and shadowed callbacks; zero is invalid, and IDs from
different programs have no shared identity. Callback metadata exposes parameter
bindings in declaration order and captures by binding ID.
The representation records nullable types and possible no-value separately;
no-value is not a host-constructible literal or a schema type.

IR inspection must retain explicit null tests, guards, and evaluation boundaries
so that translation and optimization preserve the language's semantics. Empty
filters have an explicit match-all representation. Public IR kinds, payloads,
and traversal rules are compatibility contracts, rather than parser internals.
External backends must reject unfamiliar or unsupported operations. Optimizers
cannot silently require external builders to understand a new private node kind.

### Capability selection

```go
features := capabilities.Standard().Without(capabilities.Regex)
e, err := engine.New(
    engine.WithSchema(serviceSchema),
    engine.WithCapabilities(features),
)
```

A capability Set is immutable. Standard explicitly selects the standard built-in
profile; None starts with no enabled selectable operations or methods. Allow
adds stable feature or method IDs, and Without removes them, returning a new Set.
The sets describe semantic permission and do not alter parsing. Empty-filter
match-all remains a language rule rather than a selectable operation.

The engine requires an explicit WithCapabilities or an explicitly populated
Config.Capabilities. An omitted set is a configuration error; an explicitly
empty set is valid. Unknown IDs and missing feature dependencies are configuration
errors. Dependencies are validated without silently enabling a feature the
service did not select. Built-in methods are addressable by stable method IDs;
profiles and their contents are documented so services can inspect their policy.

The accepted compatibility-profile design uses a common immutable value with
backend-specific details attached. It reports effective common capabilities,
signatures, and limits alongside portability-sensitive data and each backend's
support restrictions. A common profile does not imply that two backends
implement the same subset; callers compare both the common contract and the
selected backend details. Exact profile accessors are defined with the profile
implementation.

Extension declaration does not imply permission. A declared extension's method
ID must also be enabled. Engine configuration exposes the resolved feature set,
method signatures, and limits for discovery. Compile rejects statically selected
disabled features even if a backend could implement them. For dynamic union/Any
calls, all applicable signatures must be enabled. Explicit type guards narrow
the receiver before capability checking. Build checks target support for the
operations actually used in the program, rather than rejecting an unused
feature merely because it belongs to the engine's enabled set.

### Extension declarations and implementations

A declaration belongs to the common engine configuration and describes one
receiver signature, its receiver-call name, parameter types and arity, result
type, null behavior, and resource-cost contract. Each signature has its own
stable method ID. Non-overlapping signatures may share a call name; duplicate
IDs, attempts to replace built-ins, and ambiguous overloads are configuration
errors. Declarations are immutable after New and independent of Go evaluation
functions and backend translators.

The registration entry points use functional options:

```go
customMethod, err := hff.NewMethodDeclaration(hff.MethodConfig{
    ID:         "service.string.hasprefix",
    Name:       "hasprefix",
    Receiver:   schema.StringType,
    Parameters: []hff.Type{schema.StringType},
    Result:     schema.BoolType,
    BaseCost:   1,
})
if err != nil {
    return err
}

e, err := engine.New(
    engine.WithSchema(serviceSchema),
    engine.WithCapabilities(features.Allow(capabilities.Method(customMethod.ID()))),
    engine.WithMethods(customMethod),
)

filter, err := local.Build(ctx, program,
    local.WithMethods(local.Implement(customMethod, evaluator)),
)
```

Implementations identify the declared method ID and compatible signature.
Builders resolve each used extension against their own implementation registry.
A missing or incompatible implementation is a Build error. Implementations of
unused methods are not required. The current SQL backend has no extension
translator registration and rejects programs that use custom methods; it never
falls back to a local evaluator.

The local function signature is:

```go
type MethodFunc func(call *Call, receiver hff.Value, args []hff.Value) (hff.Value, error)
```

Call exposes context cancellation and additional resource charging. HFF handles
argument/result validation, default null propagation, and the declaration's
fixed base cost (at least one unit) before invoking a function. A declaration
may explicitly opt into invocation with null when its receiver/argument types
allow null; no-value always propagates without invoking the extension. Ordinary
conversion failures return Null() without an error. Cancellation, limit
exhaustion, and implementation-contract failures return errors. The local
implementation may charge additional steps and generated values through Call;
the base cost is not a dynamic cost callback. Callback argument storage cannot
mutate program or input storage. Implementations must not retain the call-local
meter after returning.

Local implementations operate on HFF values under a call-local context and
resource meter. They must be pure, obey their type and cost contracts, and support
concurrent calls. Data-dependent failures follow the language's null rules;
resource exhaustion and cancellation abort evaluation. The engine validates
extension results rather than allowing invalid tagged values or an incompatible
result type to enter subsequent operations. A violated implementation contract
is an extension error, not an ordinary data-dependent null result.

Translators receive typed operands and produce backend expressions using that
backend's parameter and identifier facilities. They must preserve the declared
method semantics. Registration is trusted service configuration, not a mechanism
by which a filter can introduce host functions or SQL source.

### SQL dialect and artifact contracts

The intended SQL flow binds schema paths to identifiers supplied by the service:

```go
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(
        hffsql.Bind(hff.Path{"name"}, hffsql.Column("users", "name")),
        hffsql.Bind(hff.Path{"age"}, hffsql.Column("users", "age")),
    ),
)
if err != nil {
    return err
}

// The service owns this trusted statement and its database connection.
rows, err := db.QueryContext(ctx,
    "SELECT users.id FROM users WHERE "+query.Where(), query.Args()...)
```

`hffsql` aliases `github.com/ornew/hff/backends/sql`. The result is a
predicate fragment and ordered parameters, rather than a database handle or arbitrary
statement. User literals become parameters; service-owned identifiers are quoted
for the selected dialect. A Path is a sequence of literal names, so a single key
containing a dot differs from nested field access. Unsupported operations are
Build errors, with no implicit local evaluation or database execution.

PostgreSQL is the first concrete dialect. WithDialect is required; no database
is inferred from the query or connection. SQLite, MySQL, and other dialects can
be added through the dialect extension contract after their feature mappings
have been implemented and tested. A dialect advertises its identity and supported
semantics, quotes identifier components, defines placeholder allocation and
parameter encoding, and lowers typed operations through the shared SQL builder.
The common builder owns ordered parameter accumulation and binding resolution.

Column takes separate identifier components, not SQL source. Bindings identify
literal schema paths, not dotted strings. Every referenced schema path must have
a valid binding or a supported typed access lowering. Duplicate/conflicting
bindings, unbound referenced fields, and invalid identifier components are Build
errors. No implicit conversion of a field name into an SQL fragment is allowed.

The artifact exposes Where() and Args(), owns its storage, and is reusable.
Args returns a defensive copy, including mutable parameter buffers. Parameter
encoding must preserve the represented HFF value for the selected database and
driver contract. Values whose encoding cannot preserve semantics are rejected,
not silently rounded or converted. Parameters appear in deterministic expression
traversal order, and placeholders match Args positions.

Build verifies support for each requested operation and resolved type. Supporting
an SQL operator's spelling is insufficient: null comparisons, guards, numeric
boundaries, regex semantics, and collection behavior must all be preserved.
Unsupported operations produce source-located Build errors. A backend may
provide a documented regex subset below HFF's RE2 reference (which excludes the
byte escape `\C`); its support matrix and compatibility profile must state
those restrictions, and unsupported patterns must be rejected. Dialects document
execution constraints they cannot enforce, including short-circuit order and
resource accounting. An artifact does not claim local-runtime cancellation or
step-budget guarantees for database execution; the service controls execution
through its own context and database API.

The current SQL `Config` and Build options do not express SQL runtime-budget
requirements. Program-local runtime limits are not inherited as SQL execution
guarantees. A future API that accepts an explicit execution constraint must
reject requests that the selected backend cannot enforce.

### Schema and value construction

Named field helpers include String(name), Int(name), and List(name, elementType).
Struct(fields...) builds an object schema; type descriptors such as StringType
and IntType can also be composed into union, list, and map types. Field(name,
type) provides the general form. Optional(field) permits absence, while
Nullable(field) adds null to the accepted value type. These are distinct
properties and can be combined without inserting absent keys into the record.

```go
inputSchema := schema.Struct(
    schema.String("name"),
    schema.Int("age"),
    schema.Optional(schema.Nullable(schema.String("nickname"))),
    schema.List("tags", schema.StringType),
)
record := hff.Object(map[string]hff.Value{
    "name": hff.String("Alice"),
    "age": hff.Int(18),
    "tags": hff.List(hff.String("admin")),
})
```

Value constructors preserve explicit HFF subtypes. Its zero value is invalid,
not null; Null() constructs a present null. Invalid input values are rejected
by validation, including those nested inside Any or collections. Constructors
own their storage and accessors cannot expose mutable collection buffers.

### Host values and adapters

The core accepts an explicit schema and tagged HFF values. int64 and float64,
bytes and strings, timestamps and durations, and missing versus present null
must remain distinct. No-value is an internal evaluation state that a host
cannot construct. Struct/map reflection is an optional adapter, rather than a
requirement of compilation or evaluation.

The optional Go-value adapter is a schema-configured, one-way converter. The
caller provides an explicit HFF schema and explicit mappings from HFF object
keys to direct exported Go struct fields. The adapter converts a Go record to
an owned HFF value and validates that value against the schema; it does not
infer a schema, mutate the record, or convert HFF values back to Go values.
Reflection remains outside the core explicit `Value` API. Nil containers and
wrappers map to explicit null, non-nil empty containers remain empty, and
numeric kinds are preserved without coercion. The converter snapshots its
schema, mappings, and input limits, and rejects cycles and unsupported values
with typed errors. See the [Go-value adapter guide](../guides/go-values.md) for
the implemented API and exact conversion rules.

Constructors must have documented ownership and validation rules. Configurations
are copied when an engine is created; immutable values must not expose mutable
collection storage. Concurrent calls do not authorize mutation of caller-owned
input records during validation or evaluation.

### Resource configuration

Omitted limits use documented finite DefaultLimits. WithLimits supplies
per-item overrides; zero selects the default and does not mean unlimited.
Explicit WithUnlimitedLimits is the opt-out. Negative ordinary limit values
are configuration errors. Configuration objects support the same explicit choice.
Limits cover source size, syntax depth, input depth and value count, evaluation
steps, generated elements, and JSON parse/serialization depth. `MaxJSONDepth`
defaults to 64, counts the JSON root at depth one, and applies to JSON decoding,
JSON encoding, and list/object `tostring()` output. A zero value selects the
default; a negative value is a configuration error.

Default values are established with initial implementation benchmarks and
published alongside those measurements. They are service defaults rather than
language semantics; services can tune them. Exceeding a limit never produces a
successful partial result. Context and budget enforcement within extensions is
cooperative; HFF cannot forcibly interrupt an arbitrary Go function that ignores
its contract.

### Diagnostics and cancellation

Errors must distinguish configuration rejection, common compilation rejection,
backend construction rejection, invalid host input, and aborted evaluation.
Ordinary data-dependent null results are language values, not host exceptions.
Source diagnostics retain the original query's spans through backend building.
Compilation, building, and evaluation accept context cancellation; budgets and
cancellation state are scoped to each call rather than shared across concurrent
requests.

Cancellation errors retain both the context status and `context.Cause(ctx)` in
their unwrap chain. `errors.Is` therefore matches `context.Canceled` or
`context.DeadlineExceeded`, and also matches a host cause supplied through
`context.WithCancelCause` or `context.WithDeadlineCause`. The status message
remains the standard context status. For SQL, this applies to `Build` only;
database execution is controlled by the context passed to the database API.

## Alternatives considered

A single Compile that always constructs Match is convenient for local-only use,
but couples common language checks to the cost and representation of one target.
A separate Build retains those checks and defers target-specific work.

A one-shot Eval function can be an optional convenience wrapper, but does not
replace explicit configuration and compilation reuse. Making Go reflection the
core value model would couple portable semantics to Go-specific coercion and
field-discovery rules. Database execution inside HFF would add I/O and driver
responsibilities to a filter language.

## Testing

The local extension tests cover invalid/ambiguous declarations, capability
selection, overload dispatch, null/no-value behavior, implementation matching,
result validation, charging, cancellation, panic recovery, and concurrent
calls. SQL parameter and backend tests cover the SQL-specific contracts. The
typed SQL extension contract and PostgreSQL execution guarantees are specified
in [Design Record 010](010-sql-extension-translation.md); additional dialects
remain conditional on semantics and parameter-encoding coverage.

## Remaining implementation scope

The common engine lifecycle, validated-input reuse, immutable IR access model,
explicit capability selection, and local extension declarations/evaluators are
implemented. PostgreSQL extension translators support the documented scalar
expression boundary. Additional dialects and their parameter-encoding support
matrices remain future backend work.

The record specifies behavior and public API direction; acceptance does not
claim that every package or method family has been implemented. Common
compilation and the core, scalar/string, list/object, callback, JSON, and local
extension evaluation paths, as well as base64 and temporal conversions, are
available. PostgreSQL supports its documented scalar predicate profile and
registered extension translators for supported scalar expressions. Broader SQL
operation and additional dialect support remain incomplete.
