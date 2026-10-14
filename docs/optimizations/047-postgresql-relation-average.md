# 047. Average eligible relation projections directly

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL previously materialized a relation and its projected values as JSON
before evaluating `avg()`. When the relation and callback result have a known
numeric SQL representation, the builder can instead compute a correlated
`AVG(numeric)` directly from the related rows.

Int contributions enter the aggregate as exact `numeric`. Float contributions
are converted through PostgreSQL's round-trip-safe Float text before entering
`numeric`, and the final result is checked when converted back to Float. This
requires PostgreSQL 16 or later and caller-configured `extra_float_digits > 0`;
HFF does not set or inspect the session setting. Null and guarded no-value
contributions are excluded from the aggregate. The configured relation
presence and null state still determines whether the aggregate is observable.

The direct path supports an eligible bound relation whose projection has one
statically known numeric family, including a supported callback over a bound
field, a captured outer value, or a numeric expression. A no-argument
`items:avg` also qualifies when the relation item is statically Int or Float.
A dynamic mixed or `Any` projection, a guarded/computed relation receiver, or
an ineligible binding retains the general relation materialization path.
`avg()` does not observe relation order, although a nested expression can have
its own order requirements.

## Applicability

| Relation and projection | Status | Reason |
|:--|:--|:--|
| Eligible bound relation with Int, Float, or null-only projection | Applied | Correlated `AVG(numeric)` preserves the numeric carrier and skips null contributions. |
| Statically numeric callback over a relation field or supported expression | Applied | The callback result is evaluated in the correlated row scope before aggregation. |
| Callback capturing a numeric outer column | Applied | The capture remains correlated to the current outer row. |
| Callback result with dynamic mixed or `Any` numeric family | Not applied | The generic relation materialization path retains runtime subtype recovery. |
| Guarded or computed relation receiver | Not applied | The generic path retains receiver presence and no-value semantics. |
| Nested expression with an order-sensitive operation | Partial | The average itself needs no relation order; nested operations retain their own ordering requirements. |

## Reproduction

The baseline is commit
[af373be](https://github.com/ornew/hff/commit/af373be89346a8ee62cc33999e45216e949829bc).
The candidate is the implementation accompanying this record. The focused
shape fixture was added with the candidate. Preserve a copy outside the
checkout before switching revisions, then place the same source at
`hack/observe-relation-average/main.go` and run it on both revisions. The
candidate fixture is available at
[hack/observe-relation-average/main.go](https://github.com/ornew/hff/blob/main/hack/observe-relation-average/main.go).
SQL-shape output was generated with Go 1.27.1 on Darwin arm64. The schema has
an `items` list whose struct item has a nullable Int `price` and nullable Float
`reading`, plus a required Int `threshold`; the relation correlates
`order_items.order_id` to `orders.id` and binds the two item fields to `price`
and `reading`. The fixture builds these filters:

```hff
items:avg(x=>x.price)>10.0
items:avg(x=>x.reading)>10.0
items:avg(x=>x.price+threshold)>10.0
items:avg(x=>x.price!)>10.0
items:avg(x=>threshold)>10.0
```

Run the builder-shape fixture and focused tests with Go 1.27.1:

```sh
go run ./hack/observe-relation-average
go test ./backends/sql \
  -run '^Test(RelationAverageAvoidsJSONMaterialization|NumericRelationItemAverageUsesNativeColumn)$' \
  -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run 'RelationAverage|RelationParity|RelationStateParity|UnorderedTransformParity' \
  -count=1
```

The paired SQL-shape logs are kept outside the repository at
`/private/tmp/hff-relation-avg-baseline.jsonl` and
`/private/tmp/hff-relation-avg-candidate.jsonl`. The empty-table plan fixture
uses temporary `orders(id bigint, threshold bigint)` and
`order_items(order_id bigint, price bigint, reading double precision)` tables.
It runs plain `EXPLAIN (FORMAT JSON, COSTS OFF)` for the representative
`items:avg(x=>x.price)>10.0` query without `ANALYZE`, on PostgreSQL 18.6
running on Debian aarch64 with `extra_float_digits=1`.

## Results

| Filter | Baseline `WHERE` bytes | Direct `WHERE` bytes | Args |
|:--|--:|--:|:--|
| `items:avg(x=>x.price)>10.0` | 2,710 | 487 | 4 → 1 |
| `items:avg(x=>x.reading)>10.0` | 2,900 | 495 | 4 → 1 |
| `items:avg(x=>x.price+threshold)>10.0` | 3,027 | 865 | 4 → 1 |
| `items:avg(x=>x.price!)>10.0` | 2,832 | 695 | 4 → 1 |
| `items:avg(x=>threshold)>10.0` | 2,259 | 568 | 3 → 1 |

For the first filter, the empty-table plan contained 23 nodes in the baseline
and 7 in the candidate. Both plans retained two sequential scans; the candidate
removed a row-expansion function scan and reduced nested loop and result nodes.
No timing, allocation, retained-memory, `EXPLAIN ANALYZE`, or benchmark result
was collected.

## Correctness and tradeoffs

PostgreSQL/local parity covers Int and Float relation items, empty and
all-null relations, SQL-null and missing relation state, guarded no-value
projections, null-only projections, division-by-zero failure, outer captures,
and nested relation averages. `TestPostgreSQLRelationAverageKeepsAncestorState`
also verifies flattened parent presence and null propagation. The direct path
keeps the configured parent and relation presence/null state and preserves
callback correlation. Ineligible receivers and dynamic numeric families retain
the general lowering.

For Float values, PostgreSQL converts round-trip-safe Float text to `numeric`
before aggregation. `AVG(numeric)` can differ from sequential Float addition
in cancellation, intermediate range, and rounding; the checked final Float
conversion returns null when it cannot produce a valid result. This follows
the backend-native Float Profile rather than promising bitwise agreement with
Local Float aggregation.

## Decision and limits

Use the correlated aggregate for eligible statically numeric projections.
Keep the generic relation-to-JSON path for dynamic or otherwise unrecoverable
projections and receiver states. These SQL-size and plan-shape observations
describe the tested profile only; they do not establish lower execution time
or a particular plan for populated data.
