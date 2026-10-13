# 017. Emit direct predicates for simple JSONB String-list `any()` callbacks

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

A simple `any()` callback over a directly bound JSONB `List<String>` can be
lowered through several projections even when the callback only compares its
element with a literal String. For this shape, the implementation recognizes a
callback body that is exactly equality between its bound element and a
literal String, in either operand order, then uses the same JSONB containment
predicate as direct `contains()`:

```hff
tags:any(tag=>tag="auth")
```

```sql
("records"."tags" IS NOT NULL AND
  "records"."tags" @> jsonb_build_array(($1::text)))
```

Eligibility uses resolved binding identity, not variable spelling. A callback
parameter may shadow an outer variable and still qualify when the equality
uses the callback's own parameter. For example,
`tags:any(name=>name="auth") name="Alice"` can use the direct path. If the
equality instead refers to a captured or outer binding, it retains the general
lowering.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Direct JSONB `List<String>` with item-equals-literal callback | Applied | Lowers to containment without projection barriers. |
| Nullable direct JSONB list with the same callback | Applied | The predicate retains the container null check. |
| Captured/outer binding, complex body, guards, type observation, null literal, or other representation | Not applied | Retains general callback lowering. |

## Reproduction

The SQL-shape comparison uses `tags:any(tag=>tag="auth")` and the direct
physical binding of `tags` to JSONB column `records.tags`, declared as
`List<String>`. The baseline is commit
[c5d6fed](https://github.com/ornew/hff/commit/c5d6fed); the implementation is
in `backends/sql/direct_jsonb_membership.go`, `backends/sql/scalar_where.go`,
and its focused tests. The reported lengths count generated `WHERE` expression
characters; barriers count `OFFSET 0` projections. Both forms bind one text
parameter.

Targeted unit and local/PostgreSQL parity tests passed for callback binding
identity, captures and shadowing, null/presence behavior, negation and boolean
composition, NUL rejection, and fallback observations. Full `go test ./...`,
`go vet ./...`, `go vet -tags=integration ./backends/sql`, and independent
critical review passed.

## Results

| HFF filter | Prior WHERE length / barriers / parameters | Direct WHERE length / barriers / parameters |
|:--|--:|--:|
| `tags:any(tag=>tag="auth")` | 884 characters / 7 / 1 | 84 characters / 0 / 1 |

Plain `EXPLAIN` with default planner settings on the empty temporary fixture
selected a Bitmap Index Scan with both `jsonb_ops` and `jsonb_path_ops`, using
the same `@>` index condition as `contains()`. This plan observation is
fixture-specific; no `EXPLAIN ANALYZE` or execution timing was collected, so
it does not establish a runtime speedup.

## Correctness and tradeoffs

Eligibility is restricted to the directly bound physical JSONB `List<String>`
and an equality callback that compares the actual callback item binding to a
literal String. A callback parameter may shadow an outer variable; the
resolved binding determines whether the direct path applies. Comparisons that
use a captured or outer binding, extra callback operations, guards, type
observations, null literals, metadata-backed values, plain JSON, dynamic lists,
and other element types retain the general lowering. Targeted parity checks
cover callback binding identity, nullable receivers, boolean composition,
and ineligible fallback shapes.

## Decision and limits

The implementation changes SQL construction only for the exact
equality-callback shape above. It does not rewrite arbitrary callbacks or
promise PostgreSQL short-circuiting, index selection, or execution speed.
Validate generated SQL and plans against representative indexed tables before
relying on index use.
