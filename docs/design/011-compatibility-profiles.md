# 011. Compatibility Profiles

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-12

## Summary

The accepted design exposes one immutable common compatibility profile and
backend-specific details for the selected target. The common profile describes effective
capabilities, signatures, limits, and portability-sensitive data; backend
details describe the operations and restrictions of that target. The profile
is intended for inspection and comparison, not as a claim that every backend
implements the full language.

## Motivation

An engine can enable a method that a selected backend cannot translate, and
results can depend on Unicode, regular-expression, or time-zone data versions.
Reporting only the engine's schema or capability set is therefore insufficient
to compare two deployments or explain a backend rejection.

## Goals

- Provide an immutable common profile for effective engine configuration.
- Include enabled feature and method signatures, resolved limits, and versions
  or identifiers for portability-sensitive data.
- Attach backend-specific support details without conflating them with the
  common language profile.
- Let services inspect and compare profiles without relying on ambient machine
  configuration.

## Non-goals

- Inferring equivalence from equal profile labels alone.
- Replacing source-located Build errors or backend support matrices.
- Choosing database execution policy, timeouts, or resource limits for callers.

## Design

### Public behavior and contracts

The common profile is an immutable value. Its common portion reports effective
capabilities, method signatures, resolved compile/input/runtime limits, and
portability-relevant data identifiers such as Unicode and RE2 behavior. A
backend-specific detail reports the selected backend's dialect or runtime,
supported operations, explicit restrictions, and any data version that affects
its results, including a local time-zone profile version when one is supplied.

The profile's shared portion and backend detail are inspected together. A
backend may support only a subset of the language; that subset and any regex
restriction must be explicit. In particular, HFF's regex reference excludes
the byte escape `\\C`, and a backend may support a narrower documented RE2
subset only when unsupported patterns are rejected rather than silently
changing meaning.

The profile API is common and immutable, while each backend owns its detail
type. The API exposes values rather than mutable configuration maps. It does
not discover versions from environment state during matching or SQL execution.
Exact constructors and accessors are defined with the implementation.

### Implementation and invariants

The common profile is derived from the engine's immutable configuration and
effective capability policy. Backend details are derived from the target's
declared support contract and immutable backend configuration. A local named
time-zone profile contributes its version label; UTC and fixed offsets remain
built-in and need no external data label. A caller-supplied version label
identifies selected data but does not attest to its publisher or authenticity.

### Compatibility and backend support

Adding a backend-specific detail does not change the common profile or make a
previously unsupported operation buildable. Services compare the selected
backend's detail as well as the common profile. Equality of the common profile
alone is not a guarantee of equal backend behavior.

## Alternatives considered

### Separate unrelated profile APIs for every backend

This makes backend details easy to evolve, but gives callers no common way to
inspect effective capabilities, limits, and portability data. The accepted
design keeps a common immutable portion and allows backend-owned detail types.

### One profile that assumes every backend supports the same operations

This is simpler to compare but would overstate compatibility. Backend details
must list actual support and restrictions.

## Testing

The implementation should verify immutable snapshots, consistency between the
profile and configured capabilities/limits, accurate backend support details,
and correct reporting of supplied data versions. Tests must not treat a matching
version label as proof that two backends implement the same semantics.

## Limitations and open questions

The common profile shape is accepted; exact accessor names and each backend's
detail type are implementation work. It cannot guarantee database execution
behavior controlled by the caller or attest to externally supplied data
provenance.
