# 046. Check PostgreSQL collection predicates without materializing values

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Some root collection filters observe only whether a collection is empty or its
cardinality. The translator can answer those filters from a physical JSONB
root or a correlated relation instead of decoding or aggregating the complete
collection.

For an unguarded, statically typed root JSONB `BindJSON` without type metadata,
empty List/Map/Struct equality and inequality and bare collection truth use a
shape-aware comparison with `[]` or `{}`. SQL NULL and JSON null are false in
filter position. For a List root, nonzero literal `size()` comparisons use
`jsonb_array_length` only after a CASE checks that the stored root is an array.
Zero-size comparisons reduce to an empty/nonempty shape check or a constant
filter result.

For a plain `BindRelation`, empty-list equality and inequality, bare truth, and
zero-cardinality comparisons reduce to correlated `EXISTS` or `NOT EXISTS`, a
state test, or a constant result.
The configured parent/relation presence and null state remains around the
predicate. Nonzero relation thresholds retain the count-based lowering; no
unique order is required for the zero-cardinality checks.

## Applicability

| Expression | Status | Reason |
|:--|:--|:--|
| Root JSONB List/Map/Struct without metadata: empty equality/inequality or truth | Applied | Tests the typed empty container and JSON shape. |
| Root JSONB List `size()` with an Int literal | Applied | Zero uses a shape check or constant; nonzero uses guarded `jsonb_array_length`. |
| Root JSONB Map/Struct `size()` with a nonzero Int | Not applied | Retains general lowering. |
| Plain relation: empty equality/inequality, truth, or zero `size()` | Applied | Uses existence, state, or a constant result and preserves relation state. |
| Plain relation with a nonzero `size()` threshold | Not applied | Retains count-based lowering. |
| Guarded, computed, nested, metadata-backed, or dynamic receiver | Not applied | Retains general lowering and representation checks. |

## Reproduction

The baseline is commit
[d33d9a6](https://github.com/ornew/hff/commit/d33d9a626e1c621498d64f63fa0d42b9fa18ae7b);
the candidate is the implementation in this revision.
The focused SQL-shape tests are
[`TestDirectJSONBCollectionPredicates`](https://github.com/ornew/hff/blob/main/backends/sql/direct_collection_where_test.go)
and
[`TestRelationEmptyAndZeroCardinalityUseExists`](https://github.com/ornew/hff/blob/main/backends/sql/direct_collection_where_test.go).
With Go 1.27.1, run:

```sh
go test ./backends/sql \
  -run '^(TestDirectJSONBCollectionPredicates|TestRelationEmptyAndZeroCardinalityUseExists)$' \
  -count=1
```

Focused PostgreSQL/local parity covered SQL NULL, JSON null, empty and
nonempty JSONB Lists/Maps, and nullable/missing relation state. The JSONB
integration fixture is exercised by
[`TestPostgreSQLCollectionEmptinessMatchesLocal`](https://github.com/ornew/hff/blob/main/backends/sql/direct_collection_where_integration_test.go).
The existing
[`TestPostgreSQLRelationParity`](https://github.com/ornew/hff/blob/main/backends/sql/relations_integration_test.go)
and `TestPostgreSQLRelationStateParity` fixtures include the relation
empty/truth/zero-size forms.

Run the focused JSONB integration parity with a configured PostgreSQL test
container:

```sh
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^(TestPostgreSQLCollectionEmptinessMatchesLocal|TestPostgreSQLRelation(State)?Parity)$' \
  -count=1
```

The relation parity fixtures include the empty-list, truth, and zero-size
queries for both default relation state and optional/nullable relation state.

## Results

The direct path emits empty-container comparisons or correlated existence
predicates for the listed forms. It uses guarded `jsonb_array_length` for
nonzero List cardinality comparisons. Higher relation cardinalities retain
the count-based path. No execution-time or allocation benchmark is reported.

## Correctness and tradeoffs

The JSONB shape test distinguishes SQL NULL and JSON null from an empty List
or object. A wrong JSON container shape does not match the declared collection
filter. The `CASE` protects `jsonb_array_length` from values where PostgreSQL
would raise a shape error, independent of Boolean evaluation order. Relation
filters preserve configured missing/null state; a related row counts as
present even when its item value is null.

## Decision and limits

Use these direct predicates only for the applicability rows above. Nested,
metadata-backed, guarded, computed, and dynamic values retain their previous
lowering. Higher relation counts still use the count-based path. These SQL
forms document translation behavior; they make no runtime-speed or planner
guarantee.
