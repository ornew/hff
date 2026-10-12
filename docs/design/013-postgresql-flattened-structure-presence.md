# 013. PostgreSQL Flattened Structure Presence

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-12

## Summary

When an HFF structure is stored in flattened SQL columns, the caller must
provide trusted SQL expressions that explicitly represent parent presence and
null state. HFF must not infer whether a parent object exists by checking
whether all of its child columns are null.

## Motivation

Flattening a struct into columns can erase the distinction between a null
parent, a present object whose fields are null, and optional fields that are
absent. These states affect guards, `exists()`, and field access. Child values
alone cannot reliably reconstruct the original parent state.

## Goals

- Preserve HFF null, missing, and present-object distinctions in flattened
  mappings.
- Require caller-supplied presence and null expressions wherever physical
  columns do not encode those distinctions.
- Keep expressions trusted configuration rather than query or record input.
- Reject a mapping that cannot represent the declared HFF contract faithfully.

## Non-goals

- Guessing parent presence from all-null child columns.
- Introspecting database schemas or validating stored rows during Build.
- Executing trusted expressions or managing database migrations in HFF.

## Design

### Public behavior and contracts

A flattened mapping must identify the SQL value columns and provide explicit
trusted SQL expressions for the relevant parent-presence and null states.
Optionality and nullability remain distinct: the mapping must represent absence
separately from a present SQL null when the HFF schema allows both. For a
non-nullable or required value, the declared schema may prove that a state is
impossible; it does not authorize inventing a state from child values.

The caller owns the schema, expressions, and underlying database data. `Build`
does not connect to PostgreSQL or inspect declarations or rows. An expression
is trusted backend configuration, not an escaping boundary for user-provided
SQL text. Query literals continue to use bound parameters.

### Implementation and invariants

Presence and value are separate semantic channels in HFF's typed plan and SQL
lowering. A flattened representation supplies both channels explicitly where
the database representation needs them. Child nullness cannot substitute for
parent presence, and a present all-null object remains distinct from a null
parent.

### Compatibility and backend support

Native PostgreSQL composites already provide row-null identity separately
from attribute nulls. This design applies to alternate flattened mappings that
cannot encode parent state in one composite value. Unsupported or incomplete
mappings fail during Build instead of silently choosing a presence rule.

## Alternatives considered

### Infer presence from whether all child columns are null

This requires no extra mapping, but makes a present all-null object
indistinguishable from a null parent. It is rejected because it changes HFF
semantics.

### Supply trusted presence and null expressions

This requires the caller to declare the physical representation explicitly,
but preserves the HFF states and keeps mapping policy visible at the database
boundary. This is the accepted contract.

## Testing

Tests must compare flattened translations with local evaluation for null
parents, present all-null objects, absent optional children, explicit-null
children, nested structures, and guards. Build tests must reject missing or
conflicting presence expressions and verify that query literals remain bound.

## Limitations and open questions

The explicit-expression policy is implemented by `sql.BindStruct`. Field
bindings are relative to the bound struct; nested structs use nested
`BindStruct` values with their own presence and null expressions. Required
fields need bindings, while an unbound optional field remains absent. See the
[flattened PostgreSQL struct guide](../backends/postgresql/flattened-structures.md)
for the API and supported child binding forms.
