# 014. PostgreSQL Relation Bindings

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-12

## Summary

PostgreSQL relations are described by explicit immutable caller-owned bindings.
A relation binding supplies the correlation between the current record and
related rows, plus the HFF schema of each related item. HFF lowers supported
collection predicates to parameterized SQL and leaves database execution to
the caller.

## Motivation

Relation access cannot be inferred safely from a query field name or arbitrary
database schema. The caller knows how records are related and which columns
form an HFF item. Making that mapping explicit lets HFF construct a supported
predicate without taking ownership of joins, connections, or execution.

## Goals

- Bind relations explicitly to trusted SQL identifiers and correlation
  expressions.
- Describe related items with HFF types so compilation and SQL lowering can
  preserve field, null, and missing-value behavior.
- Keep relation bindings immutable and reusable across builds.
- Preserve HFF's collection result behavior for translated existential and
  collection predicates.
- Require unique ordering only for operations whose result depends on item
  order; `any()` and `all()` do not require ordering.
- Keep SQL parameterized and database I/O caller-owned.

## Non-goals

- Discovering relations, foreign keys, or item schemas from the database.
- Accepting arbitrary user-query SQL fragments as relation definitions.
- Opening connections, executing queries, or controlling database timeouts.
- Requiring order for predicates whose result is independent of row order.

## Design

### Public behavior and contracts

A relation is an immutable mapping from an HFF path to a caller-selected SQL
relation, correlation, and typed item shape. The SQL source is trusted
configuration; query values remain bound parameters. A relation mapping must
make the row correlation and item type explicit enough for the backend to
translate a supported operation without guessing from runtime column values.

Collection operations that depend on order require the relation to specify a
unique order. Operations such as `any()` and `all()` are order-independent and
must not require an ordering clause. If a required relation property is absent
or the operation cannot be translated faithfully, Build returns a source-located
backend error.

The relation and its metadata are copied or otherwise immutable after
construction. The caller remains responsible for database schema, data
consistency, execution, transactions, and timeouts. HFF does not validate the
physical relation during Build.

### Implementation and invariants

Common compilation retains HFF type and capability checks. The SQL backend
resolves each relation binding during Build, lowers supported predicates using
the declared item schema, and binds query literals through the normal ordered
parameter path. Translation must preserve null/no-value behavior and cannot
assume a database execution order or enforce local runtime budgets that the
database does not expose.

### Compatibility and backend support

Relation support is backend-specific. A PostgreSQL relation binding does not
imply that another SQL dialect supports the same operations. Each backend must
advertise its supported collection operations and ordering requirements.

## Alternatives considered

### Infer relations from table names and foreign keys

This can reduce configuration, but makes query meaning depend on mutable
database introspection and conventions. Explicit caller-owned bindings are
chosen for predictable service contracts.

### Accept arbitrary SQL source for correlation

Raw source is concise but cannot be validated as a typed mapping and risks
confusing trusted service configuration with user query input. The accepted
design uses an explicit immutable relation descriptor and documented trusted
SQL expression inputs.

## Testing

Tests should cover correlation, typed field access, null and missing values,
empty relations, any/all truth tables, capability rejection, and order-dependent
operations with non-unique ordering rejected. Real PostgreSQL parity tests must
compare completed results with the local backend while making no claim that the
database evaluates rows in HFF callback order.

## Limitations and open questions

The immutable relation contract and ordering rule are accepted. Exact public
field names, expression constructors, and the supported relation-operation
matrix are implementation work; this record does not imply that relation
bindings are currently available.
