# 054. Skip guard-result reconstruction for immediate root observers

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

A JSON value can be guarded as a numeric-containing List or Map, then consumed
immediately by a root observer such as `type()`, `exists()`, or `size()`.
Ordinary lowering normalizes the guarded collection to JSON even though the
observer needs only the full guard result, the root category, or collection
size. For this bounded shape, the builder still validates the ordinary value
path and preserves its diagnostics, but the emitted query evaluates the source
and full guard without rebuilding the normalized payload. Existing shortcuts
for declared identity guards remain unchanged.

The optimization can skip reconstruction only for the final numeric-containing
List/Map type guard when its result immediately feeds `type()`, `exists()`,
`size()`, or list `count()`. Root Union guard targets remain on ordinary
lowering. Any earlier guards still normalize their outputs. A later guard, a
value consumer such as `json()` or `string()`, predicate `count()`, or a
carrier without a proven JSON representation keeps ordinary lowering for the
final guard. A deferred value is eligible when ordinary lowering produces a
proven JSON carrier and the other conditions hold.
JSONB still requires numeric type metadata whenever the guard must observe an
Int/Float distinction lost in storage.

## Applicability

| Shape | Status | Reason |
|:--|:--|:--|
| Final numeric List/Map guard followed immediately by a root observer | Applied | Earlier guards retain normal output; the root observer does not consume the final normalized payload. |
| Eligible numeric guard followed by another guard or value consumer | Not applied | The later operation requires the final guard's normalized output. |
| Root Union targets, predicate `count()`, carriers without a proven JSON representation, or uncertain source shape | Not applied | These do not meet the immediate-root proof. |
| Local | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [58ad777](https://github.com/ornew/hff/commit/58ad777b629c115e1d010f3e9e95e1deb64125b0).
The candidate is the implementation containing this record. The SQL-shape
fixture uses an `Any` field named `data` bound as JSON. Go 1.27.1 ran on
Darwin arm64; PostgreSQL 18.6 was used for plain plan inspection.

Run the focused Go tests:

```sh
go test ./backends/sql -run '^TestRootNumericGuardObservers' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLRoot' -count=1
```

The native JSON SQL-shape cases are `data:[list[float]]:exists`,
`data:[list[int]]:size=2`, `data:[map[float]]:type="object"`, and
`data:[map[list[float]]]:size=2`. The focused PostgreSQL test
`TestPostgreSQLRootNumericGuardObservers` also checks root observers, value
consumers, and nested guard behavior across JSON and JSONB. Its numeric guard
matrix passed 532 query-record results; related root observer checks cover
guard chains, selective numeric restoration, empty/missing values, and
parameter inference.

Supplementary generated SQL and plan output are retained outside the
repository at `/private/tmp/hff-root-guard-observer-{before,after}-{0,1,2,3}.sql`
and `/private/tmp/hff-root-guard-observer-{before,after}-explain.log`.

## Results

| Native JSON expression | Baseline `WHERE` bytes | Candidate `WHERE` bytes | Change | Uncertainty |
|:--|--:|--:|--:|:--|
| `data:[list[float]]:exists` | 4,831 | 1,107 | −3,724 bytes | Generated SQL size only |
| `data:[list[int]]:size=2` | 3,797 | 1,617 | −2,180 bytes | Generated SQL size only |
| `data:[map[float]]:type="object"` | 4,926 | 1,434 | −3,492 bytes | Generated SQL size only |
| `data:[map[list[float]]]:size=2` | 6,870 | 2,039 | −4,831 bytes | Generated SQL size only |

For the List Float `exists` query, plain `EXPLAIN` showed one `Aggregate` node
before and none after, with 35 plan lines before and 15 after. The plan still
contains function scans used by the full guard validation. Other expression
shapes were not inspected with `EXPLAIN`. Guard-chain and JSON-value consumer
SQL remained byte-identical. These observations describe SQL shape and plan
placement only; no execution timing, allocation, `EXPLAIN ANALYZE`, or
runtime-speed result was collected.

## Correctness and tradeoffs

The source expression and complete guard remain in the query. Only the final
normalized payload reconstruction is omitted when the immediate observer does
not consume it. Tests cover JSON and JSONB guard results, JSONB metadata,
decoder failures, null and missing values, cancellation, and controls that
still require normalized output. `Build` continues to validate expressions
that the emitted observer path does not consume.

## Decision and limits

Use this path only for the immediate root observers listed above after a final
numeric-containing List/Map guard. Earlier guards keep their normalized output;
later guards, serialized values, callback predicates, and any case whose output
depends on the final normalized collection keep the ordinary lowering. The
extra validation can increase Build work; the SQL-size and plain-plan
observations do not establish a runtime benefit.
