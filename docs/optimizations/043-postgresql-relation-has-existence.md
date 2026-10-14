# 043. Lower relation endpoint checks to existence predicates

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

For a plain bound SQL relation, the constant Int checks `has(0)` and `has(-1)`
only need to know whether at least one related row exists. The translator can
use the same correlated `EXISTS` witness for both checks rather than count all
related rows or compute an indexed element. The surrounding parent and
relation presence/null case remains in place:

```sql
CASE
  WHEN parent_is_missing OR relation_is_null THEN NULL::boolean
  ELSE EXISTS (
    SELECT 1 FROM relation_items AS item
    WHERE item.parent_id = parent.id
  )
END
```

This does not require a unique order because either endpoint check is true
exactly when the list is nonempty. The database may choose any physical plan;
the lowering does not prescribe scan order or early termination.

## Applicability

| Expression | Status | Reason |
|:--|:--|:--|
| Plain SQL relation with constant Int `has(0)` or `has(-1)` | Applied | Uses the correlated existence witness and preserves relation state. |
| Other indexes, including constant `has(-2)` | Not applied | They retain cardinality/index lowering. |
| Invalid Float indexes | Not applied | They retain the general lowering and its result behavior. |
| Computed or guarded relation values | Not applied directly | These retain their existing list-materialization path. |

## Reproduction

The baseline is commit
[b23b3c0](https://github.com/ornew/hff/commit/b23b3c0). The candidate is the
implementation represented by this record. The durable paired test is
[`TestPostgreSQLRelationHasEndIndicesMatchLocal`](https://github.com/ornew/hff/blob/main/backends/sql/relation_has_existence_integration_test.go).
With Go 1.27.1 and a PostgreSQL test container, run:

```sh
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLRelationHasEndIndicesMatchLocal$' -count=1
```

The test covered eight query forms over six fixtures, for 48 PostgreSQL/local
comparisons. Cases included nonempty, empty, null, and missing relation state;
a relation containing a null-valued item; negation; null observation; the
`has(-2)` fallback; and an invalid Float index. The SQL package unit tests
also passed.

The plain `EXPLAIN` diagnostic used empty temporary parent and item tables with
a B-tree index on the child correlation key:

```sql
BEGIN;
CREATE TEMP TABLE hff_relation_parents (
    id bigint, items_present boolean, items_null boolean
);
CREATE TEMP TABLE hff_relation_items (parent_id bigint, value bigint);
CREATE INDEX hff_relation_items_parent_idx
    ON hff_relation_items (parent_id);
SET LOCAL enable_seqscan=off;
EXPLAIN SELECT records.id
FROM hff_relation_parents AS records
WHERE COALESCE(
    (CASE
      WHEN (records.items_present) IS NOT TRUE OR (records.items_null) IS TRUE
        THEN NULL::boolean
      ELSE EXISTS (
        SELECT 1 FROM hff_relation_items AS item
        WHERE item.parent_id=records.id
      )
    END),
    FALSE
);
ROLLBACK;
```

The empty-table plan used a hashed SubPlan with a Bitmap Heap Scan and Bitmap
Index Scan on the child correlation index; it contained no aggregate or
`COUNT`. Sequential scans were disabled for this diagnostic.

## Results

No execution-time or allocation benchmark was collected. The plan only shows
the shape selected for this empty-table fixture; it does not establish
per-parent early termination, runtime speed, or plan selection for populated
tables.

## Correctness and tradeoffs

The paired fixtures preserve the relation's parent and relation
presence/null behavior. Empty relations do not match either endpoint check;
a null-valued item still makes the relation nonempty. Null or missing
relations remain governed by their existing state expressions.
Other constant indexes, including `has(-2)`, and invalid Float indexes keep
the previous cardinality/general path.

## Decision and limits

Use the existence witness only for constant Int `has(0)` and `has(-1)` on a
plain bound relation. For example, `has(-2)` remains on the general path. No
unique order is needed for the optimized checks. The checked
plan is not a timing result and does not predict plans for other schemas or
data.
