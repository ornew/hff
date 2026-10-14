# 033. Specialize exact mixed-numeric literal thresholds

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

A direct comparison between a native Int or Float column and an opposite-kind
numeric literal could use the general mixed Int/Float comparison kernel. The
translator now derives an exact direct threshold for eligible pure filter
comparisons, without rounding the values being compared:

- For an Int column and a Float literal, Build uses the exact represented
  Float value to derive an integer boundary. For example, `i>18.5` becomes
  `i>=19`; `i=18.5` is false for every non-null Int, and
  `i!=18.5` becomes a non-null test.
- For a Float column and an Int literal exactly representable as Float, the
  direct Float predicate uses that Float parameter. For an Int that is not
  exactly representable, Build uses the neighboring Float boundary that
  preserves comparison against the original exact Int. For example,
  `f>9007199254740993` becomes `f>=9007199254740994`.

The rewrite covers all six comparisons. Nullable-column checks preserve the
existing filter and `NOT` behavior; only parameters needed by the surviving
thresholds remain, in source order. Existing same-Float comparisons remain on
their direct path. Guarded, dynamic, JSON, and value-context comparisons use
the general lowering. The column must be on the left; permitted literal-left
comparisons inside value expressions retain general lowering. Eligible
identity-derived column expressions use the existing direct path.
Integer-spelled closed `number()`
conversions may fold to an Int literal; Float-looking text remains on
PostgreSQL's native input path. Conversion failure and NUL checks are
unchanged.

## Applicability

| Comparison | Status | Reason |
|:--|:--|:--|
| Native Int column and Float literal in a pure filter comparison | Applied | Derive exact integer thresholds from the represented Float value. |
| Native Float column and Int literal in a pure filter comparison | Applied | Use a direct Float parameter when exact; otherwise use adjacent Float bounds. |
| Same-kind native Float column and Float literal | Already direct | Preserve the existing direct Float predicate path. |
| Guarded, dynamic, JSON, or value-context operands | Not applied | Retain the general lowering and its presence and representation handling. |
| Float-looking string conversion to `number()` | Not applied | PostgreSQL's native Float input and conversion-failure checks remain in force. |

## Reproduction

The baseline is commit
[4f32b2e](https://github.com/ornew/hff/commit/4f32b2e17935f23f558866f71d7a271065164c7a).
The candidate implementation accompanies this record. The temporary shape
fixture used Go 1.27.1 and PostgreSQL 18.6 on aarch64 with
`extra_float_digits=1`. The bindings were nullable native `bigint` field `i`
and nullable native `double precision` field `f`. The queries
included both column/literal numeric-type combinations, their compositions
with `AND`, `OR`, and `NOT`, and same-kind Float and Int controls.

The integration suite used 14 Int inputs, 22 Float inputs, 14 Float literals, and
9 Int literals across the six comparisons. It included raw literals and
integer-spelled `number()` conversions, guards, null inputs, and 14 additional
contexts covering logical composition, identity casts, and fallback paths.
The exact threshold unit matrix contains 3,192 rational comparison
cases. The PostgreSQL/local integration suite passed 22,808 results on both
baseline and candidate at `extra_float_digits=1` and `3`.

Durable fixtures are the [exact rational comparison unit tests](https://github.com/ornew/hff/blob/main/backends/sql/literal_numeric_comparison_test.go),
[eligibility and parameter tests](https://github.com/ornew/hff/blob/main/backends/sql/literal_numeric_threshold_test.go),
and [PostgreSQL parity tests](https://github.com/ornew/hff/blob/main/backends/sql/literal_numeric_threshold_integration_test.go).
The 17-case SQL-shape generator and raw SQL/plans were temporary files outside
the repository. The shape measurements count generated `WHERE` bytes and
`OFFSET 0` barriers; they are not benchmark results.

The focused checks were:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^(TestMixedLiteralThresholdsMatchExactRationalOrdering|TestMixedLiteralThresholdsRejectOtherOperations|TestDirectMixedLiteralThresholdParameters|TestMixedLiteralThresholdsKeepValueAndGuardFallback|TestMixedLiteralThresholdsPreserveJSONAndConversionChecks)$' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLExactLiteralThresholdsMatchLocal$' -count=1
```

## Results

| Filter | Baseline bytes / barriers | Candidate bytes / barriers | Bound parameter |
|:--|--:|--:|:--|
| `i>18.5` | 886 / 4 | 61 / 0 | Float `18.5` becomes Int `19` |
| `f>9007199254740993` | 806 / 4 | 71 / 0 | Int becomes Float `9007199254740994` |
| `i>18.5 f>9007199254740993` | 1,788 / 9 | 139 / 0 | Both thresholds, in source order |
| `i=18.5` | 886 / 4 | 5 / 0 | Constant false; no parameter |
| `i!=18.5` | 887 / 4 | 27 / 0 | Non-null predicate; no parameter |
| Same-kind, guarded, value-context, and dynamic controls | unchanged | unchanged | Existing path |

With default planner settings, plain `EXPLAIN (COSTS OFF)` on the empty shape
fixture changed Nested Loop/`Result` counts from 3/4 to 0/0 for `i>18.5`
and from 8/9 to 0/0 for the combined filter. These are plan-shape observations
only.

A separate indexed fixture used `SET enable_seqscan=off` as a diagnostic
to inspect candidate index conditions. The Int threshold exposed
`i IS NOT NULL AND i >= 19`; the large-Int Float threshold exposed
`f IS NOT NULL AND f >= 9007199254740994`. The baseline plans had
Bitmap Index Scans without those index conditions. This forced-plan fixture
shows predicate eligibility, not default planner choice, execution cost, or
speed. No `EXPLAIN ANALYZE` or timing benchmark was run.

## Correctness and tradeoffs

The 22,808 PostgreSQL/local results passed on both the pinned baseline and
candidate. The matrix covers all six operators, Int64 and Float64 boundaries,
fractional thresholds, signed zero, nullable columns, `NOT`, guards, source
parameter order, and fallback contexts. Unit tests also cover subtype and
parameter selection, retained JSON lowering, NUL and conversion checks, and direct
Float behavior. Full repository tests, `go vet`, integration-tagged vet, SQL
race checks, and independent review passed.

The SQL is shorter for eligible literal comparisons, but this does not
establish lower execution time. The indexed fixture required disabling
sequential scans and is not evidence of index selection under ordinary
planner settings.

## Decision and limits

Use exact Build-time thresholds only for eligible direct filter comparisons
between a native Int/Float column and a typed numeric literal. Preserve the
general mixed comparison for guarded, dynamic, JSON, and value-context
expressions. The implementation does not change HFF numeric semantics or
Float literal parsing.
