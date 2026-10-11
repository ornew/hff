# 005: Explicit Type Guards

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

HFF adds the postfix expression `value:[Type]` to check a value against an
explicit type constraint. The guard preserves the original value when it
matches and produces internal no-value when it does not; it never converts the
value. A following access or method call is resolved using the narrowed type.
The guard is selected by the `access:type-guard` capability.

## Motivation

Dynamic values such as `Any` and unions allow a service to accept heterogeneous
records. A method call on such a value can have several statically applicable
receiver-family signatures. Requiring permission for every candidate is
necessary for predictable compile-time capability enforcement, but it should
not force a service to enable a family it intends to exclude. Explicit type
guards let the query state which type may proceed to an operation.

## Goals

- Let a query narrow a value to an explicit type without conversion.
- Make mismatches participate in the existing internal no-value semantics.
- Check subsequent method capabilities against the narrowed type.
- Keep capability enforcement static and portable to local and translated
  backends.
- Preserve the existing `type()` method as a string-valued runtime inspection
  method without special flow-analysis behavior.

## Non-goals

- Implicit type inference across separate predicates or branches.
- Runtime capability errors or treating a disabled method as null.
- Renaming methods to encode receiver families.
- Adding conversion, defaulting, or exception-catching behavior to a type guard.

## Design

### Syntax and type expressions

The syntax is `value:[Type]`. The colon inside the brackets distinguishes a
type guard from ordinary indexing. It applies to any value expression, including
computed expressions, and can be followed by field, index, guard, or method
operations.

The primitive type spellings are `any`, `null`, `bool`, `number`,
`int`, `float`, `string`, `bytes`, `timestamp`, `duration`, `object`, and
`list`. `int` and `float` select the `int64` and `float64` numeric subtypes.
`list[T]` and `map[T]` constrain collection elements or values. `T|U` forms a
union, with parentheses available to group union expressions. A guard
constraint is intersected with the expression's known type so existing schema
precision is retained, while nullable members explicitly named by the
constraint remain represented.

### Runtime and capability behavior

On a match, the guard returns the original value unchanged. On a mismatch, it
returns internal no-value. An already no-value operand remains no-value. Normal
no-value propagation then applies to access, arithmetic, ordinary calls,
comparisons, boolean conversion, and collections. Existing method exceptions
such as `exists()`, `type()`, `tojson()`, and `tostring()` retain their specified
behavior for no-value.

Method resolution after a guard uses its narrowed result type. For example,
`value:[string]:contains("x")` requires the string-family method capability;
for a non-string runtime value, the guard fails and the call is skipped. Without
a guard, a call on a union or `Any` requires every statically applicable method
signature to be enabled. A capability-disabled candidate is a compile-time
error, not a runtime error or null result. A value known only as null has no
non-null family to select a method signature, so all statically applicable
candidates are checked. For a nullable typed value such as `T|null`, non-null
candidate resolution uses `T`, and null follows the ordinary null propagation
rule.

The existing `type()` method continues to return a string such as `number` or
`string`. It does not narrow another expression and does not suppress
capability checks.

### Program representation and backends

The compiler represents the operation as an appended `TypeGuardNode` with one
child, the guarded value, and a `Constraint` type payload. The node's `Type()`
is the intersection of the receiver's known type with the constraint, preserving
known structural or element precision and nullable members. The public `Node`
accessor `Constraint() (Type, bool)` returns the exact user-specified
constraint, which the runtime checker uses. A type guard may produce no-value,
so `MayNoValue()` is true. Existing node kind numeric values remain stable.

The feature capability ID is `access:type-guard`. Backends must preserve the
guard's value-preserving success and no-value failure behavior. A backend that
cannot preserve the result must reject a program containing the node as
unsupported.

## Alternatives considered

- **Runtime capability error for a disabled dynamic family:** this makes the
  outcome depend on record values and cannot be represented by every backend.
  Capability violations remain compile-time errors instead.
- **Return null for a disabled dynamic family:** this can make a forbidden
  operation look like ordinary missing data. A guard provides an explicit
  source-level check while retaining distinct no-value semantics.
- **Different names for each receiver family:** this avoids overload ambiguity
  but fragments the method vocabulary and changes query syntax unnecessarily.
- **Infer narrowing from `type()` comparisons or logical control flow:** this
  makes capability permission depend on analyzer-specific branch reasoning.
  The explicit postfix guard carries the narrowed type directly in the
  expression and IR.
- **Convert to the requested type:** conversion changes data and requires
  conversion-failure rules. The guard only checks and preserves the value.

## Testing

Conformance tests should cover matching and mismatching primitive, numeric,
nullable, union, list, and map constraints; guards on computed expressions;
preservation of the exact input value; no-value propagation through each
expression context; narrowing before method capability checks; disabled
candidate rejection without a guard; and stable IR kind, child order, span, and
constraint accessor behavior. Local and translated backends should share these
cases, and unsupported backends must reject the node rather than change its
meaning.

## Limitations and open questions

The common compiler implements the type expression grammar and explicit guard
semantics. Local evaluation and SQL backends remain separate implementation
stages and must preserve these contracts.
