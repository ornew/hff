# 041. Reuse native identity results and proven presence

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Some PostgreSQL expressions projected a value again even when a built-in
operation returned its input unchanged, or projected a separate presence value
that was already statically `TRUE`. The translator can now reuse an existing
operand projection for Int- and Null-valued `round()`, `ceil()`, and `floor()`
views and for native Duration `nanoseconds()`. The identity view retains the
operand's no-value state; SQL `NULL` remains a value-level null.

The translator also removes tautological presence conditions when the later
operand's presence is proven `TRUE` in eligible arithmetic, collection, JSON
access, regex, and method expressions. Presence depending on a guard or runtime value
remains explicit. Float rounding remains computed according to the PostgreSQL
Float profile. Dynamic JSON values retain their decoding; a subsequent native
Int identity view can still reuse that decoded result.

## Applicability

| Expression | Status | Reason |
|:--|:--|:--|
| Int or Null `round()`, `ceil()`, or `floor()` over an already projected operand | Applied | These views preserve the operand value and reuse its carrier and presence. |
| Native Duration `nanoseconds()` over an already projected Duration | Applied | Duration nanoseconds are already represented by the native Int carrier. |
| Later operand whose no-value presence is proven `TRUE` | Applied | Removes a tautological condition and retains the earlier operand's presence. |
| Float rounding, JSON `Any` or union decoding, or runtime-dependent presence | Not applied directly | These paths still require computation, decoding, or an explicit presence result; native identity views after decoding remain eligible. |
| Guard-derived presence | Retained | An identity view may remove a value projection, but the guard's no-value behavior remains visible. |

## Reproduction

The baseline is commit
[b739cf4](https://github.com/ornew/hff/commit/b739cf4ffd46af49b6e4f90076368e81efd965bd).
The fixture used Go 1.27.1 on Darwin/arm64 and PostgreSQL 18.6 on
Debian/aarch64 with `extra_float_digits=1`. It measured generated `WHERE`
bytes, `OFFSET 0` barriers, and argument counts; no timing benchmark or
execution-time measurement was made.

The paired PostgreSQL/local fixture evaluated 49 filters over 121 records,
for 5,929 comparisons on both the baseline and candidate. Its Int inputs
include null, both int64 limits, negatives, zero, and positives. It includes
callback captures, Boolean short-circuiting, and dynamic Duration parsing with
valid and invalid strings. The temporary shape fixture used nullable and
required native Int, Float, String, Bool, Bytes, and Duration columns, plus a
`List<Int>` and `Map<Any>` JSON binding. Representative queries were:

```hff
i:round:json=i:floor:json
i:ceil:floor:round:json=i:json
d:nanoseconds:json=i:json
d!:nanoseconds:json=d:nanoseconds:json
data.n:[int]:round:json=i:json
items:any(x=>x:round+1>i)
i+2>10
p:and(q):type="bool"
```

The durable paired test is `TestPostgreSQLNativeOperationReuseMatchesLocal` in
[`native_operation_cache_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/native_operation_cache_integration_test.go).
Run it with:

```sh
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLNativeOperationReuseMatchesLocal$'
```

The shape generator and emitted SQL were temporary artifacts. Its table and
bindings followed the [native operation reuse fixture](040-postgresql-native-operation-reuse.md#reproduction)
without the paired test's `words` column. The queries and binding families
above describe the measured SQL-shape inputs; the paired integration fixture
is the semantic comparison. Plain `EXPLAIN` was run on an empty temporary
table without `ANALYZE`.

## Results

Generated `WHERE` byte counts and `OFFSET 0` barriers for representative
nullable-profile cases:

| Filter | Baseline bytes / barriers | Candidate bytes / barriers |
|:--|--:|--:|
| `i:round:json=i:floor:json` | 465 / 5 | 331 / 3 |
| `i:ceil:floor:round:json` | 532 / 6 | 331 / 3 |
| `d:nanoseconds:json=i:json` | 398 / 4 | 331 / 3 |
| `d!:nanoseconds:json=d:nanoseconds:json` | 620 / 6 | 468 / 4 |
| `data.n:[int]:round:json=i:json` | 1,345 / 10 | 1,258 / 9 |
| `items:any(x=>x:round+1>i)` | 1,292 / 10 | 1,169 / 9 |
| `i+2>10` | 477 / 3 | 421 / 3 |
| `p:and(q):type="bool"` | 503 / 3 | 403 / 3 |

Argument counts were unchanged for each case. Plain plans on the empty fixture
had fewer nested-loop nodes for the first Int rounding filter (four to two),
the chained Int views (five to two), native Duration view (three to two),
guarded Duration case (five to three), and dynamic JSON case (nine to eight).
These plan observations are fixture-specific; they do not establish execution
speed or behavior on populated tables.

## Correctness and tradeoffs

The paired fixture passed all 5,929 PostgreSQL/local comparisons against both
baseline and candidate. Controls confirmed that Float `round()` remains
computed, dynamic JSON `Any` values retain their ordinary parsing,
and guard-derived presence remains explicit. An extension observing
`pg_typeof` confirmed that Int identity views retain a `bigint` carrier and
Float rounding retains `double precision`; an invalid numeric conversion
followed by `round()` retains its folded-null carrier. The identity aliases
preserve the original carrier and presence, and the simplification applies
only when presence is statically known to be `TRUE`.

## Decision and limits

Reuse only the listed identity views and statically proven presence values.
Other computed methods and runtime-dependent presence retain their existing
lowering. SQL-shape and plain-plan observations are not runtime-speed claims.
