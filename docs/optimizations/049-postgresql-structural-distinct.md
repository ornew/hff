# 049. Group JSON-native structural distinct values once

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

For structural List, Map, and Struct element trees, PostgreSQL previously
compared each candidate item with earlier items to implement stable
`distinct()`. For eligible JSON-native trees, the translator can instead
compute one grouping key per item, select the earliest ordinal in each group,
and return the original value. Scalar element grouping continues to use its
existing typed keys and is outside this change.

If the declared element tree can contain only JSON-native scalars, Lists, Maps,
and Structs and has no possible Float or `Any`, the key is the normalized item
as `jsonb`. If Float or `Any` is possible, a recursive walk builds a key from
each node's full path, JSON category, and scalar value. Numeric values use the
shared exact numeric ordering key so equal Int and Float values group together
without rounding Int values through Float. Full paths distinguish array
indexes from object keys and safely encode arbitrary keys. Null nodes,
container categories, key sets, and nested structure remain represented in
the key. The selected result is still the original JSON value, preserving the
first occurrence's subtype and representation when recoverable.

This grouping is used only after the existing schema, representation, and
JSONB metadata checks. Tagged Bytes, Duration, and Timestamp alternatives
conservatively keep the existing type-aware pairwise HFF equality path. A
Float that is unrepresentable under the PostgreSQL Float Profile
in an `Any` tree receives an ordinal-specific key, preserving its existing
non-reflexive equality behavior; a statically typed invalid Float is normalized
to null before grouping.

## Applicability

| Element tree | Status | Reason |
|:--|:--|:--|
| Structural List, Map, or Struct tree without Float or `Any` | Applied | Normalized JSONB equality is a sufficient structural key. |
| Structural tree with possible Float or `Any` | Applied | Full-path category keys preserve structure; numeric leaves use exact numeric keys. |
| Scalar element grouping | Not changed | Existing typed scalar grouping remains in use. |
| JSONB with recoverable numeric subtype metadata | Applied | Existing metadata restoration remains before key construction. |
| Tree containing tagged Bytes, Duration, or Timestamp | Not applied | Tagged string types keep pairwise HFF equality. |
| JSONB without required representation metadata | Not applied | Existing Build-time recoverability checks still reject insufficient bindings. |

## Reproduction

The baseline is [2588bc7](https://github.com/ornew/hff/commit/2588bc7b9d549db0282e11b0ad8fcec7e8e5f044).
The candidate is the commit that contains this record and the implementation.
The durable shape fixture is
[`hack/observe-structural-distinct/main.go`](https://github.com/ornew/hff/blob/main/hack/observe-structural-distinct/main.go).
It builds `items:distinct:size>0` for these element types and both JSON
formats:

- `Struct{n:Int}`
- `List<Int|Float>`
- `Any`
- `Struct{payload:Bytes}` as a fallback control

Run the fixture on each revision and compare `sql_bytes` and `args` in its
JSON-lines output. To reproduce the baseline, first copy
`backends/sql/structural_distinct_integration_test.go` and the shape fixture
outside the checkout. Check out the baseline, copy the integration test into
that checkout, and run the same integration expectations; then run the copied
shape fixture. This keeps expected results independent of the baseline source
while applying identical inputs. Go 1.27.1 was used for SQL generation. The
shape and plain `EXPLAIN` fixtures used PostgreSQL 18.6 on Debian aarch64 with
`extra_float_digits=1`; plans were collected on empty tables and not executed
for timing. Raw baseline/candidate output and plans are retained outside the
repository under `/private/tmp/hff-structural-distinct-*`.

The candidate's focused correctness commands were:

```sh
go test ./backends/sql -run 'TestPostgreSQLStructuralDistinctGrouping|TestPostgreSQLSelectionRequiresJSONBMetadata|TestPostgreSQLSelectionRetainsRecoveredStorageTypes' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run 'TestPostgreSQLStructuralDistinctMatchesLocal|TestPostgreSQLStructuralDistinctRejectedFloatLeaves' -count=1
```

The candidate passed 242 parity results and eight raw-JSON edge cases. The
pinned baseline passed the 234 parity cases present in its copied fixture and
the same eight raw-JSON edge cases; four additional subtype cases were added
to the candidate fixture. Coverage includes nested list/object structure,
object-key order and path boundaries, exact mixed numeric equality, first
subtype and signed-zero retention, and invalid Float behavior.

The existing ordered-relation, nested-relation, and relation-state parity
controls also passed 750 query/record comparisons covering relation order and
parent null or missing state.

## Results

`WHERE` byte counts were measured using `len(q.Where())`:

| Element tree | Format | Baseline bytes | Candidate bytes | Args, baseline → candidate | Plain EXPLAIN nodes, baseline → candidate |
|:--|:--|--:|--:|:--:|:--:|
| `Struct{n:Int}` | JSON | 3,420 | 2,083 | 3 → 2 | 28 → 25; joins 1 → 0 |
| `List<Int\|Float>` | JSON | 11,564 | 5,216 | 1 → 1 | 66 → 36; joins 2 → 0 |
| `Any` | JSON | 5,837 | 3,322 | 1 → 1 | 60 → 34; joins 1 → 0 |
| `Struct{n:Int}` | JSONB | 5,116 | 3,774 | 3 → 2 | 44 → 41; joins 1 → 0 |
| `List<Int\|Float>` | JSONB | 13,260 | 6,927 | 1 → 1 | 82 → 52; joins 2 → 0 |
| `Any` | JSONB | 7,530 | 5,031 | 1 → 1 | 76 → 50; joins 1 → 0 |
| `Struct{payload:Bytes}` fallback | JSON | 5,663 | 5,663 | 3 → 3 | 28 → 28; joins 1 → 1 |
| `Struct{payload:Bytes}` fallback | JSONB | 7,359 | 7,359 | 3 → 3 | 44 → 44; joins 1 → 1 |

The table reports SQL size and the listed plan observations, not execution
time, allocation, or memory. The plans came from empty fixtures and do not
establish a runtime improvement or a plan for populated tables. Tagged-family
fallback controls were unchanged. Scalar element-grouping paths were not
changed or measured here.

## Correctness and tradeoffs

The grouping key is constructed only for trees whose declared types establish
the JSON-native categories it needs. The translator retains the original
ordinal and value, so `distinct()` still returns the first HFF-equal item.
Object keys are grouped by actual key and not object serialization order.
Integer and Float values compare by represented numeric value; the numeric
key avoids loss of Int precision. JSONB metadata remains necessary when its
stored representation has erased an observable subtype.

The fallback keeps the established pairwise equality logic for tagged string
families. The recursive key for Float/Any trees adds per-item traversal and
does not prove that execution is faster for every input size or shape. Plain
`EXPLAIN` showed fewer joins and plan nodes for the measured JSON-native
structural cases, but no execution timing or benchmark was collected.

## Decision and limits

Use one structural key per eligible JSON-native item, then select the earliest
ordinal for each key. Keep the existing pairwise path for tagged families and
retain all normalization, metadata, and Float validity checks. The SQL-shape
results apply only to the listed schemas and do not justify a general speed
claim.
