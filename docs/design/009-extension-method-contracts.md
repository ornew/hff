# 009. Extension Method Contracts

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

Extension methods are declared as immutable typed signatures in shared engine
configuration, separately from their local evaluator or backend translator. Each
signature has a stable capability ID. Extension implementations follow HFF's
type, null, and resource contracts rather than introducing backend-specific
language behavior.

## Motivation

Applications need to add service-specific pure operations while retaining
compile-time type checking, capability controls, backend support validation,
and predictable resource accounting. A declaration cannot be coupled to one
Go callback because the same program may be built for different backends.

## Goals

- Keep declarations immutable after engine construction and separate them from
  implementation registries.
- Give every signature its own stable ID for explicit capability selection.
- Allow non-overlapping signatures to share a call name while rejecting
  ambiguous overloads.
- Preserve HFF's default null and no-value behavior unless a declaration
  explicitly opts into null invocation.
- Bound extension work with a fixed base cost and call-local additional charges.

## Non-goals

- Extensions cannot replace built-in signatures or supply SQL source strings.
- A registration does not automatically grant capability permission.
- The cost model does not accept an arbitrary dynamic cost callback.
- Backend support does not imply that a local Go evaluator is registered, or
  vice versa.

## Design

### Public behavior and contracts

A declaration describes one receiver signature, call name, parameter types and
arity, result type, null policy, and base cost. Its stable ID identifies that
signature. Different signatures may share a receiver-call name when their
applicable receiver and argument types do not overlap. Duplicate IDs, attempts
to replace built-ins, and ambiguous overloads are configuration errors.

The public API creates a declaration with
`hff.NewMethodDeclaration(hff.MethodConfig{...})`. The config contains `ID`,
`Name`, `Receiver`, required `Parameters`, trailing `Optional` types, an
optional repeated `Variadic` type, `Result`, `NullPolicy`, and `BaseCost`.
Required arguments come first, then optional positions, then the variadic tail;
optional positions cannot be skipped to supply a variadic value.

Declarations belong to common engine configuration through
`engine.WithMethods` and remain immutable after engine construction. Declaring a
method does not enable it: `capabilities.Method(declaration.ID())` must also be
present in the selected capability set. The local backend registers evaluators
with `local.WithMethods(local.Implement(declaration, function))`. Its builder
requires an exact matching implementation for every used declaration; missing
or incompatible implementations are source-located `hff.BackendError`s. SQL
extension translators are not yet available.

The default `hff.PropagateNull` policy propagates a null receiver or argument
without invoking the extension. Once null leaves no viable signature, remaining
arguments are not evaluated. A declaration may use `hff.InvokeNull` to opt into
invocation with null, but the receiver and arguments must still satisfy
declared types that admit null. No-value always propagates and never reaches an
extension. It is an internal evaluation state and cannot be constructed as a
host `Value`.

Each declaration specifies a fixed base cost of at least one unit, charged
before invocation. `local.Call` exposes `Context()`, `ChargeSteps(n)`, and
`ChargeGeneratedValues(n)`. The evaluator must meter data-dependent work and
generated values, including values reused from immutable inputs. Charge
failures are latched even if ignored. The base is not a dynamic cost function.
Context cancellation and resource exhaustion abort evaluation; a partial value
is never returned as a successful result. Returned values are physically
validated, checked against `MaxInputDepth`, and required to match the declared
result type unless the result is ordinary null, which is always an allowed data
failure.

### Implementation and invariants

The engine owns copied declaration data and exposes the resolved signatures for
capability and portability discovery. The local backend resolves a used
signature to its registered evaluator. SQL backends will resolve used
signatures to typed translators when that contract is implemented. Local
implementations are pure, concurrency-safe, and cannot retain the call-local
resource meter after returning.

### Compatibility and backend support

Existing built-ins retain their signatures and capability IDs. Extension IDs
are stable within the service's declaration contract. An extension may be
available in one backend and unavailable in another; each builder rejects a
used extension without a compatible implementation and does not fall back to
another backend.

## Alternatives considered

### One ID for a group of overloads

A group ID is convenient for broad enablement but cannot select or discover
individual signatures precisely. Per-signature IDs keep capability checks and
profiles explicit.

### Dynamic per-call cost functions

A user callback could estimate work, but executing that callback adds a second
extension surface and permits costs that cannot be validated before execution.
A fixed nonzero base plus explicit call-local additional charges is auditable
and lets the evaluator meter data-dependent work.

## Testing

Implementation tests must cover duplicate and ambiguous declarations, stable
per-signature IDs, capability denial, shared-call-name non-overlap, null
propagation and explicit null invocation, no-value bypass, fixed base charging,
additional step/generated-value exhaustion, cancellation, missing backend
implementations, result validation, and concurrent reuse.

## Current backend boundary

The local evaluator API is implemented as described above. Extension
translation for SQL and additional SQL dialects remain separate backend work.
