# 007. Structural Equality Capabilities

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

HFF separates permission to compare list contents from permission to compare
object contents. `comparison:list-equality` and
`comparison:object-equality` select those permissions; the
`group:structural-equality` group selects both. Compilation requires only the
permissions for structurally comparable types that may occur in an expression,
including recursively nested values.

## Motivation

Structural equality is useful for filters, membership checks, and uniqueness,
but a service may want to enable one composite family without enabling the
other. Requiring one indivisible permission would prevent policies such as
allowing object comparisons while prohibiting list comparisons. Runtime-only
checks would also make a disabled operation appear input-dependent and could
allow a query to compile despite using a forbidden comparison.

## Goals

- Keep equality behavior independent of the selected backend.
- Let services independently enable list and object structural equality.
- Detect statically possible structural comparisons during compilation,
  including nested contents and `Any` or union types.
- Let explicit type guards narrow the required permission set.
- Keep scalar comparisons and explicit null tests independent of structural
  equality permissions.

## Design

### Public behavior and contracts

The three capability IDs are:

- `comparison:list-equality`: recursively compare list values by length and
  element order.
- `comparison:object-equality`: recursively compare object values by key set
  and associated values.
- `group:structural-equality`: select both family permissions.

The family permissions apply to `=`, `!=`, `in()`, `list:contains()`, and
`list:unique()` whenever the compared values may be lists or objects. A
structural comparison of nested contents requires the permission for each
compatible composite family reached recursively. Different value families are
unequal; their contents are not compared.

Compilation checks the types that can be compared at each expression. For
example, `tags:unique` where `tags` is a list of strings needs neither family
permission, but comparing `tags` itself with a list literal requires list
equality. A list of objects requires object equality when `unique()` compares
its elements. `Any` and union types require every statically possible composite
permission. `value:[list[string]]:unique` narrows an `Any` value to scalar list
elements and requires no structural permission. A failed guard produces
no-value as usual; it does not convert or enable a disallowed operation.

Scalar comparisons and explicit comparisons with a null literal are exempt.
This exemption follows HFF's explicit null-test semantics; an arbitrary
expression that happens to evaluate to null is not thereby reclassified as a
null test. A statically possible structural comparison without its permission
is a compile-time capability error. Runtime values cannot turn a disabled
comparison into null or a runtime capability error.

`Standard()` includes both family permissions. Allowing the group adds both;
removing the group removes both. Each family can subsequently be selected
independently.

### Compatibility and backend support

The language's equality, membership, and uniqueness results do not change.
Capability sets become more precise: applications may use the combined group
for the prior all-enabled behavior, or select either structural family alone.
Each backend must preserve structural comparison semantics or reject a program
whose operations it cannot implement. Capability permission does not by itself
promise backend support.

### Implementation and invariants

The compiler derives needed permissions from possible operand types. It recurses
through compatible nested list and object contents and does not require a
permission for incompatible value families that compare unequal. Guards refine
the operand type before this check. The runtime continues to use ordinary HFF
structural equality; capabilities do not change equality results.

### Testing

Conformance covers family-independent selection, the combined group, scalar and
explicit-null exemptions, nested list/object contents, `Any` and union values,
guards, equality/inequality, membership, containment, and uniqueness. Capability
failures retain the source span of the comparison requiring permission.

## Alternatives considered

### One structural-equality permission

A single permission is simpler but cannot let a service distinguish list
comparisons from object comparisons. The combined group preserves easy
all-enabled configuration while the family permissions provide finer control.

### Runtime capability rejection or null

Runtime rejection depends on the input value and makes static policy harder to
inspect. Returning null would hide that a forbidden operation was attempted.
Compilation instead rejects a query if any statically possible structural
comparison lacks permission.

## Limitations and open questions

The permissions describe language-level structural equality. Backends still
need to publish which comparisons they can translate and reject unsupported
operations. Their execution resource limits remain backend-specific.
