# 044. Lower eligible scalar predicates inside relation callbacks

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

For a plain PostgreSQL relation consumed by `any()` or `all()`, callbacks over
direct native scalar item columns can otherwise add `LATERAL` projections
inside the correlated subquery. When the callback is composed from direct
native scalar fields, literals, and eligible scalar predicates combined with
Boolean `AND`, `OR`, and `NOT`, the translator can reuse direct scalar
predicate lowering in the subquery.
Relation correlation, parent and relation state, and callback semantics remain
in place.

For example, the callback
`items:any(item=>item.price>100 item.active)` can remove four inner
`CROSS JOIN LATERAL` projections from the generated predicate. Two outer
generic projections remain; this change does not remove every projection in
the complete filter.

## Applicability

| Callback shape | Status | Reason |
|:--|:--|:--|
| Direct native scalar fields and literals combined with eligible predicates and Boolean `AND`/`OR`/`NOT` | Applied | Eligible predicates lower directly in the correlated subquery. |
| Nullable or optional struct item state | Not applied | The item state retains general lowering. |
| Noneligible callback expression | Not applied | The whole callback retains its general lowering path. |
| Computed relation list or guarded callback scope | Not applied directly | These keep their existing materialization and state handling. |

## Reproduction

Use the relation setup in the [PostgreSQL relation guide](../backends/postgresql/relations.md#bind-the-row-source-and-compile-a-filter), with this filter:

```hff
items:any(item=>item.price>100 item.active)
```

The baseline is commit
[d3780216](https://github.com/ornew/hff/commit/d3780216fed04225185794d188553514304600ca).
Compare the generated predicate's SQL shape: the eligible callback had four
inner `CROSS JOIN LATERAL` projections before this lowering and has none after
it. Two outer generic projections remain.

With Go 1.27.1 and a PostgreSQL test container, focused parity checks passed:

```sh
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^(TestPostgreSQLTypedRelationCorrelation|TestPostgreSQLRelationParity|TestPostgreSQLNestedRelationParity|TestPostgreSQLRelationStateParity|TestPostgreSQLRelationAliasCaptureParity)$' \
  -count=1
```

`TestPostgreSQLTypedRelationCorrelation` covered six query forms and table
expectations. The other named tests compare PostgreSQL results with local
evaluation for relation, nested-relation, relation-state, and alias-capture
cases. A focused six-form fixture covers `any()`, guarded `any()`, an indexed
control, `all()`, `any()` over a nullable Bool item, and `all()` with `OR`; it
includes a valid nullable Bool row. No runtime benchmark or speed measurement
was run.

## Results

| Workload | Baseline | Candidate | Change | Scope |
|:--|--:|--:|--:|:--|
| `items:any(item=>item.price>100 item.active)` | 4 inner projections | 0 inner projections | -4 | Two outer generic projections remain. |

On a populated temporary fixture with 100 parent rows and 1,000 item rows,
plain `EXPLAIN` for the representative callback selected a Hash Right Semi
Join with a sequential scan of items and a filter for `active` and `price >
100`; the plan had no inner `LATERAL` steps. This is one planner choice, not a
runtime measurement or a guarantee for other schemas, data, or statistics.

## Correctness and tradeoffs

The lowering keeps the relation's correlation predicate in the subquery and
preserves its existing parent/relation presence and null handling. It does not
promise a physical callback evaluation order or database short-circuiting.
Nullable or optional struct item state and noneligible callback expressions
retain general lowering.

## Decision and limits

Use direct scalar predicates within a relation callback only for eligible
native item fields and literal expressions composed with Boolean
`AND`/`OR`/`NOT`. This does not make otherwise unsupported scalar operations
eligible. Keep the existing general path wherever callback item state or
expressions do not meet that proof.
