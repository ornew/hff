# 018. Lower direct predicates for native Float columns

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Eligible predicates on directly bound native Float columns previously passed
through `LATERAL` projections with `OFFSET 0` barriers. The direct path compares
the column with same-Float literals and supports scalar truth and null tests.
It also lowers `in()` when the receiver and literal needles are Float. Mixed
Int/Float comparisons retain the exact general path; computed expressions,
guards, JSON, and structured representations are not eligible.

The direct comparisons preserve Float64 behavior for schema-valid finite Float
values, including signed-zero equality, subnormal values, adjacent
representable values, and maximum finite values. Nullable input handling
remains explicit, while no-value and null behavior match the general lowering.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Direct native Float column compared with a Float literal | Applied | Emits a direct column predicate without projection barriers. |
| Direct native Float truth/null tests and Float-literal `in()` | Applied | These scalar forms use the direct path when their values are statically known. |
| Mixed Int/Float comparisons | Not applied | Exact cross-subtype comparison remains on the general path. |
| Computed or guarded values, JSON, and structured representations | Not applied | The general lowering preserves expression and representation semantics. |

## Reproduction

The baseline is commit [d448008](https://github.com/ornew/hff/commit/d448008).
The comparison is between the previous projection lowering and the direct
Float predicates. Representative filters are `factor<1.5`, `factor=-0.0`,
`!factor<1.5`, and `age>18 factor<1.5`. The comparisons keep the same bound
parameters; lengths count generated `WHERE` characters and barriers count
`OFFSET 0` projections. The candidate lowering is implemented in
`backends/sql/scalar_where.go`.

Unit and targeted local/PostgreSQL parity tests passed for positive and
negative zero, the minimum subnormal, adjacent Float values, maximum finite
Float, and large-Int mixed-comparison regressions. Full `go test ./...`,
`go vet ./...`, and `go vet -tags=integration ./backends/sql` passed.

Plain `EXPLAIN` with default planner settings on an empty temporary indexed
Float table showed a Seq Scan with a SubPlan/Nested Loop for the baseline and
a Bitmap Index Scan for the direct `factor<1.5` predicate. Its index condition
included `factor IS NOT NULL AND factor < 1.5::double precision`. This
observation is limited to that fixture. No `EXPLAIN ANALYZE` or execution
timing was collected.

## Results

| HFF filter | Prior WHERE length / barriers | Direct WHERE length / barriers |
|:--|--:|--:|
| `factor<1.5` | 208 characters / 2 | 80 characters / 0 |
| `factor=-0.0` | 208 characters / 2 | 80 characters / 0 |
| `!factor<1.5` | 298 characters / 3 | 86 characters / 0 |
| `age>18 factor<1.5` | 499 characters / 5 | 151 characters / 0 |

These are SQL-shape observations, not runtime measurements. The direct form
removes projection barriers in the listed cases, but this does not establish
faster execution. The plan observation above applies only to the tested
`factor<1.5` predicate and empty temporary fixture.

## Correctness and tradeoffs

The direct path is limited to a native Float binding and statically known
Float operands. In filter context, a nullable receiver is false; `in()` treats
null needles as non-matches, and every non-null needle must be Float for the
direct path. Mixed Int/Float operations, computed operands, guards, JSON, and
structured fields retain the existing general lowering so it can preserve
exact cross-type semantics and representation recovery. Parity coverage also
includes the mixed Int/Float fallback near large integer boundaries.

## Decision and limits

Use the direct path only for the eligible native Float shapes above. The SQL
length and barrier counts do not predict PostgreSQL plan choice or execution
time. The single fixture plan does not establish index use on other schemas or
data, and controlled measurements would be needed before making speed claims.
