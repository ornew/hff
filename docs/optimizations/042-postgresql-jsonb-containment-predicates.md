# 042. Lower simple JSONB filters with containment

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Simple predicates over a directly bound JSONB object or list can be expressed
with PostgreSQL's containment operators while retaining HFF's filter behavior.
For a root object with no type metadata, a single literal-key equality against
a String or Bool value uses `@>` and a constructed one-member object. For a
root `List<String>`, an `all()` callback whose body is equality between its
item and one String literal uses `<@` and a one-element array:

```sql
payload IS NOT NULL AND payload @> jsonb_build_object($1, $2)
tags IS NOT NULL AND tags <@ jsonb_build_array($1)
```

The explicit non-null filter mask makes a SQL-null receiver a non-match. For
the object predicate, a missing key, JSON-null value, or unequal String/Bool
value also does not match. For the list predicate, empty-list truth is
preserved; duplicate elements do not change the result. Keys and values remain
parameters rather than SQL text.

## Applicability

| Expression | Status | Reason |
|:--|:--|:--|
| Direct root JSONB object, one literal key, String or Bool literal equality, no metadata | Applied | Uses `@>` with a non-null mask; key and value are bound. |
| Direct root JSONB `List<String>`, non-nullable elements, simple literal equality in `all()` | Applied | Uses `<@`; empty lists match and duplicates are immaterial. |
| Null-equality, inequality, numeric equality, nested-key path, JSON text, computed object, or metadata-backed object | Not applied | These forms retain the general lowering. |
| Nullable list elements or a more complex `all()` callback | Not applied | These forms retain the general lowering. |

The supported object path is limited to one literal key and String/Bool values.
This does not generalize to containment for JSONB numeric values, whose HFF
representation can require metadata.

## Reproduction

The baseline is commit
[f057366](https://github.com/ornew/hff/commit/f057366), before these direct
containment paths. The candidate is the implementation represented by this
record. The paired integration test is named
`TestPostgreSQLDirectJSONBPredicatesMatchLocal`. With Go 1.27.1 and a
PostgreSQL test container, run:

```sh
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLDirectJSONBPredicatesMatchLocal$' -count=1
```

The test covered 11 query forms over seven fixtures: 77 PostgreSQL/local
comparisons. Cases included SQL NULL, JSON null, missing keys, duplicate list
values, escaped and Unicode keys, Bool false, and filter negation. Direct-path
eligibility unit tests also passed. String literals containing NUL are
rejected.

The plain `EXPLAIN` diagnostic used an empty temporary table and GIN indexes:

```sql
BEGIN;
CREATE TEMP TABLE hff_containment_fixture (payload jsonb, tags jsonb);
CREATE INDEX hff_containment_payload_idx
  ON hff_containment_fixture USING GIN (payload);
CREATE INDEX hff_containment_tags_idx
  ON hff_containment_fixture USING GIN (tags);
SET LOCAL enable_seqscan=off;
EXPLAIN SELECT 1 FROM hff_containment_fixture
 WHERE payload IS NOT NULL
   AND payload @> jsonb_build_object('team', 'auth');
EXPLAIN SELECT 1 FROM hff_containment_fixture
 WHERE tags IS NOT NULL
   AND tags <@ jsonb_build_array('auth');
ROLLBACK;
```

Object `@>` produced a Bitmap Index Scan. List `<@` remained a Seq Scan even
with sequential scans disabled. No rows were inserted, and the diagnostic used
neither `ANALYZE` nor timing measurements.

## Results

No execution-time or allocation benchmark was collected. The `EXPLAIN` result
only shows that the tested object `@>` operator exposed a GIN index condition;
the tested list `<@` operator did not. This does not promise index selection
for other schemas, operator classes, statistics, or data.

## Correctness and tradeoffs

Both paths keep SQL-null receivers from matching at the filter boundary. The
object equality form distinguishes a missing key and JSON null from a matching
String or Bool literal. The list form only applies to non-nullable String
elements and the simple equality callback, preserving empty-list truth and
duplicate-insensitive containment. All other shapes retain general lowering.

## Decision and limits

Use containment only for the listed direct-root JSONB forms. The checked GIN
plan is fixture-specific, and list `<@` did not expose an index condition in
that fixture. No runtime-speed claim follows from these plans or SQL forms.
