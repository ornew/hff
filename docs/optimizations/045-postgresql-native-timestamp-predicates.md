# 045. Compare native PostgreSQL Timestamp bindings physically

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

Native `BindTimestamp` comparisons previously reconstructed each stored value
as numeric Unix-epoch nanoseconds, even for a filter comparing directly to a
Timestamp literal or another native bound Timestamp. For eligible scalar
filters, the translator now compares the physical `timestamptz` base and the
optional nanosecond remainder in chronological order. Native `before()` and
`after()` use the same physical comparison path.

With a remainder, a literal is represented by its microsecond-floor
`timestamptz` value and a 0–999 remainder parameter. The comparison checks the
base instant first and then the remainder when bases are equal. This preserves
nanosecond distinctions without extracting epoch nanoseconds.

## Applicability

| Expression | Status | Reason |
|:--|:--|:--|
| Native `BindTimestamp` scalar comparison with a Timestamp literal | Applied | Compares the physical base and optional remainder directly. |
| Native comparison between two eligible `BindTimestamp` columns | Applied | Uses the same chronological ordering of base and remainder. |
| Native `before()` or `after()` filter | Applied | Uses the corresponding direct physical comparison. |
| Guarded, structured, or value-context expression | Not applied | Retains the existing numeric conversion/general lowering. |
| JSON/JSONB Timestamp value | Not applied | Uses its separate schema-known canonical-string representation. |

## Reproduction

The baseline is commit
[383f70f9](https://github.com/ornew/hff/commit/383f70f9de7da63ac547120923d87637c7e05f70).
The durable paired test is
[`TestPostgreSQLPhysicalTimestampComparisonsMatchLocal`](https://github.com/ornew/hff/blob/main/backends/sql/timestamp_integration_test.go).
With Go 1.27.1 and a PostgreSQL test container, run:

```sh
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLPhysicalTimestampComparisonsMatchLocal$' -count=1
```

The test covered 12 query forms over seven rows, for 84 PostgreSQL/local
comparisons. Cases included paired NULLs, a NULL comparison side, timestamps
with the same microsecond base and different remainders, exact equality, a
pre-epoch nanosecond, and year 0000. The existing stored-Timestamp parity test
also passed with the direct comparison forms.

For plan shape, the temporary table contained 2,000 rows generated at
one-second intervals from `2026-09-30 23:30:00+00`, each with a 123 ns
remainder, and a B-tree index on `created_at`. The fixture and direct
predicate diagnostic were:

```sql
BEGIN;
CREATE TEMP TABLE events AS
    SELECT g AS id,
           '2026-09-30 23:30:00+00'::timestamptz
             + g * interval '1 second' AS created_at,
           123::smallint AS created_ns
    FROM generate_series(0, 1999) AS g;
CREATE INDEX events_created_at_idx ON events(created_at);
ANALYZE events;
PREPARE hff_direct_timestamp (timestamptz, smallint) AS
    SELECT id FROM events WHERE
    ("events"."created_at" IS NOT NULL AND (
        "events"."created_at" > ($1::timestamptz)
        OR (
            "events"."created_at" = ($1::timestamptz)
            AND "events"."created_ns" > ($2::smallint)
        )
    ));
EXPLAIN EXECUTE hff_direct_timestamp(
    '2026-10-01 00:00:00.000000+00', 123
);
-- Representative old scalar shape, not the complete generated baseline WHERE:
EXPLAIN SELECT id FROM events WHERE
    ((EXTRACT(EPOCH FROM events.created_at)::numeric * 1000000000::numeric)
        + events.created_ns::numeric)
        > '1790812800000000123'::numeric;
ROLLBACK;
```

The builder's parameterized predicate for
`created > "2026-10-01T00:00:00.000000123Z":timestamp` was:

```sql
("events"."created_at" IS NOT NULL AND (
    "events"."created_at" > ($1::timestamptz)
    OR (
        "events"."created_at" = ($1::timestamptz)
        AND "events"."created_ns" > ($2::smallint)
    )
))
```

The ordered arguments were the microsecond-floor instant
`2026-10-01 00:00:00.000000+00` and remainder `123`. Plain `EXPLAIN` selected
a Bitmap Heap Scan with a BitmapOr of two Bitmap Index Scans. The baseline
comparison used the representative scalar expression
`((EXTRACT(EPOCH FROM events.created_at)::numeric * 1000000000::numeric) + events.created_ns::numeric) > '1790812800000000123'::numeric`, which selected a
sequential scan. This baseline expression is a representative old scalar
shape, not the exact generated baseline predicate.

## Results

| Filter | Baseline shape | Direct shape | Observation |
|:--|:--|:--|:--|
| `created > "2026-10-01T00:00:00.000000123Z":timestamp` | Numeric epoch-nanosecond reconstruction | Physical base/remainder comparison | Bitmap Index Scans appeared for the direct form in the indexed fixture. |

No `EXPLAIN ANALYZE`, runtime timing, or benchmark was collected. The index
plan is specific to the fixture and does not predict plans for other schemas,
indexes, or data distributions.

## Correctness and tradeoffs

The physical storage contract is unchanged: the `timestamptz` base is floored
to a microsecond and an optional `smallint` stores the remainder from 0 to 999.
The caller guarantees that a bound pair is NULL together and that the
remainder is in range; without a remainder, stored values must be
microsecond-exact. This also preserves pre-epoch ordering and HFF year 0000's
mapping to PostgreSQL 1 BC. SQL NULL behavior remains explicit in the filter.

## Decision and limits

Use physical comparison only for eligible native `BindTimestamp` filter
predicates and `before()`/`after()`. Guarded, structured/value-context, and
JSON/JSONB expressions retain their existing paths. This record establishes
SQL shape and parity, not a runtime speedup or universal index use.
