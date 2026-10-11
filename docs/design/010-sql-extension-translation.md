# 010. SQL Extension Translation

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

An SQL extension is a dialect-specific, build-time translator for one immutable
HFF method declaration. It receives typed expressions scoped to a translator
call and constructs SQL from trusted templates and bound scalar values. HFF
continues to own signature selection, capabilities, null/no-value behavior,
parameter ordering, and source diagnostics. SQL execution and timeouts remain
the responsibility of the calling service.

## Motivation

The same HFF program can be built for multiple backends, but an extension may
have an implementation for only some of them. Passing raw query strings into
an extension would bypass parameter binding and expose backend internals. A
typed, call-scoped construction API gives translators access to their
arguments while keeping SQL artifacts owned by the backend.

## Goals

- Register a translator against an exact method declaration and selected SQL
  dialect.
- Keep SQL construction pure, parameterized, immutable, and safe for concurrent
  builds.
- Preserve core HFF overload, capability, null, no-value, and argument-order
  semantics around translated expressions.
- Let callers inspect backend-enforceable execution guarantees and explicitly
  request limits; reject requests the selected backend cannot enforce.
- Keep SQL text an implementation artifact and keep database I/O outside HFF.

## Non-goals

- Arbitrary SQL parsing, SQL injection filtering, or verification that a
  developer-authored template implements the declared method's semantics.
- SQL database connections, execution, pooling, or database timeout policy.
- Runtime registration of arbitrary third-party dialect implementations.
- Claiming that the PostgreSQL backend enforces local HFF evaluation budgets.

## Design

### Translator registration

The extension declaration and SQL translator are separate registrations:

```go
translator := sql.Implement(declaration, sql.PostgreSQL,
    func(call *sql.Call, receiver sql.Expression, args []sql.Expression) (sql.Expression, error) {
        suffix, err := call.Parameter(hff.String("!"))
        if err != nil {
            return sql.Expression{}, err
        }
        return call.Expression(schema.StringType, "{} || {}", receiver, suffix)
    },
)
query, err := sql.Build(ctx, program,
    sql.WithDialect(sql.PostgreSQL),
    sql.WithMethods(translator),
)
```

`sql.Implement` pairs one immutable `hff.MethodDeclaration` with one dialect
and a translation function. `sql.WithMethods` copies the supplied registration
slice into builder configuration. Build requires an exact declaration match
for each used extension signature and rejects missing, incompatible, duplicate,
or wrong-dialect registrations. The dialect must be explicitly selected;
PostgreSQL is currently the only supported dialect.

The translator receives `*sql.Call`, a typed receiver, and typed arguments.
Expressions are immutable and scoped to their call. Helpers may be used
concurrently during the translation, but a translator must not retain the
Call after returning. The Call exposes its context and dialect, `Parameter`
for dialect-validated bound scalar values, and `Expression` for building a
typed expression from a trusted developer-authored template. Each `{}` in a
template corresponds to one supplied expression. Templates cannot contain
`$N` placeholders; literal values must be bound with `Parameter`.

HFF validates expression ownership and the declared scalar type. It does not
parse templates or validate that their SQL implements the declared operation.
Templates are therefore trusted code, not a place for query or record input.
Translators must preserve the declaration's behavior on their supported domain
and return an error when faithful translation is unavailable. Build converts
translation failures into source-located backend errors. It performs no SQL
execution or database I/O.

HFF retains method-family dispatch and wraps translated branches with core
presence, null, and type-selection behavior. No-value never reaches the
translator as an HFF value. Null behavior follows the declaration's policy and
nullable types. SQL expression and bound parameter storage are owned by the
resulting Query; `Where` and `Args` expose the predicate and an ordered,
defensive parameter slice.

### Execution guarantees

`sql.ExecutionGuarantees(dialect)` reports what a dialect can enforce for
HFF's per-record `MaxSteps` and `MaxGeneratedValues` budgets. A built Query also
exposes `Guarantees()`. `sql.WithExecutionLimits` adds explicit positive
requests to Build configuration. A zero field asks for no guarantee; a
negative value is a configuration error. If a requested positive limit is not
enforceable, Build returns a backend error instead of ignoring the request.

PostgreSQL currently reports neither guarantee. The Program's local runtime
budgets do not become SQL execution constraints. Database-level timeouts and
cancellation are configured by the service through its driver and context.
The SQL builder does not claim a specific evaluation order or local
short-circuit work count.

### Compatibility and backend support

PostgreSQL extension translators are limited to the existing typed scalar
expression profile. They do not extend support to collections, JSON, temporal
values, or other types rejected by PostgreSQL Build. Unsupported built-in or
extension semantics fail Build with a source-located backend error; they do not
fall back to local evaluation.

Adding a dialect requires a concrete semantics matrix and parameter-encoding
coverage, including parity tests for null, type guards, numeric boundaries,
string ordering/regex restrictions where applicable, and extension methods.
The current contract does not promise a user-pluggable dialect registry.

## Alternatives considered

### Give translators the entire public IR

An IR visitor would expose every operation shape to each translator and make
the backend responsible for dispatch and parameter ownership. Typed expressions
and a scoped Call helper provide only the construction surface needed for a
single method translation while core HFF retains language dispatch.

### Allow raw SQL strings as translator results

Raw fragments cannot express parameter ownership or prevent mixing data with
SQL syntax. Templates with typed expression placeholders plus a dedicated
parameter helper keep the boundary explicit. This does not prove template
semantics; templates must remain trusted developer code.

### Document execution limits without a request API

Documentation alone cannot stop a caller from assuming a requested budget was
enforced. The builder exposes enforceable guarantees and rejects explicit
positive requests that it cannot honor. Database timeouts remain a separate
caller-owned setting.

## Testing

Tests must cover declaration and dialect matching, missing/duplicate
registrations, overloads, expression ownership, placeholder counts, forbidden
`$N` templates, parameter ordering and encoding, null/no-value and guard
dispatch, source spans on translator errors and panics, cancellation, concurrent
Build calls, and explicit execution-limit rejection. PostgreSQL differential
tests must compare translated results against local HFF semantics over scalar
boundaries and extension fixtures. The site example verifies construction only;
tagged integration tests verify live PostgreSQL behavior.

## Limitations and open questions

PostgreSQL is the only supported SQL dialect. Its extension translators may
construct only supported scalar expressions and cannot ask Build to validate
template semantics. PostgreSQL cannot enforce HFF `MaxSteps` or
`MaxGeneratedValues`; services that need database runtime bounds must configure
them through their database integration.
