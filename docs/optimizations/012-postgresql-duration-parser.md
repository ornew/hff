# 012. Read validated duration fractions as ASCII bytes

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

The PostgreSQL duration parser sums fractional components one decimal digit at
a time so that nanosecond integrality and int64 bounds remain exact for long
fractions. The candidate converts each already-validated ASCII fraction to
UTF-8 bytes once, then reads digits by byte offset in the recursive carry
query. It retains the existing grammar, component summation, integrality and
overflow checks, and null propagation.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| PostgreSQL `duration()` with fractional components | Applied | Fractional digits use byte indexing after validation; exact summation and result checks are retained. |
| Integer-unit and compound-unit durations | Partial | These parse through the same SQL expression; measured compound inputs show a small execution regression. |
| String predicate control | Not applicable | It does not call the duration parser and keeps byte-identical SQL. |
| Local evaluation and other database workloads | Not applicable | Measurements cover the selected HFF fixtures and PostgreSQL profile only. |

## Reproduction

The baseline is [ef9945b](https://github.com/ornew/hff/commit/ef9945b969e60ec6a5af322145491143847ea299). The candidate `backends/sql/duration.go` source snapshot has SHA-256 `9eba6b1d5ed1e28c2596766baeefa503d0cbf430bf1b0f09838cd00b918124aa`. The same benchmark harness was used for both source versions:

- [Build benchmark](https://github.com/ornew/hff/blob/main/backends/sql/duration_bench_test.go), SHA-256 `e5c4dd065e2adb2a430c216c5615cc8f33bd91537d7205ed1e4bcf7c62dcb04b`
- [Execution benchmark](https://github.com/ornew/hff/blob/main/backends/sql/duration_execution_bench_test.go), SHA-256 `ed55515e863a11c5cc436e35536038fd1ed2d8d61cc6299456310f7660e169c9`

Build covers four query shapes—`StringControl`, `IntegerUnits`, `Repeated512`,
and `Serialized512`—for native text, JSON, and JSONB. Its inputs do not vary by
stored fraction length. Database execution also covers `CompoundUnits`,
fractions with 16, 512, and 4,096 digits, and a 20,000-digit fraction. The
20,000-digit database case has one positive row. Two-row fixtures contain
positive and negative values; 25-row fixtures also include invalid, zero, and
null values. Fractional fixtures combine tiny decimal components that sum
exactly to one nanosecond, with alternating negative cases. Each execution
sample compares SQL-matched IDs with local HFF matches before collecting
PostgreSQL planning and execution fields.

Build command:

```sh
TMPDIR=/private/tmp GOMAXPROCS=16 GOTOOLCHAIN=go1.27.1 \
go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLDurationBuild$' -benchmem \
  -benchtime=200ms -count=3
```

PostgreSQL execution command (use a separate plan directory for each stage):

```sh
TMPDIR=/private/tmp GOMAXPROCS=16 \
HFF_POSTGRES_CONTAINER=hff-pg-conformance-20261012 \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-duration-evidence/baseline-plans \
GOTOOLCHAIN=go1.27.1 \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLDurationExecution$' -benchtime=1x -count=3
```

Change the plan directory for the candidate and repeated stages. Use the same
PostgreSQL 18.6 profile when reproducing the comparison.

Runs were serialized in this order: baseline, candidate, baseline repeat,
candidate repeat. Each stage has three samples, for six measurements per side.
The host was Darwin 25.2.0 arm64, Apple M3 Max with 64 GiB RAM. Go was 1.27.1
with `GOMAXPROCS=16`. PostgreSQL was 18.6 in a Debian aarch64 container using
image digest `sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`.
The container had no network, explicit CPU, memory, or cpuset limits; its
database directory used tmpfs. JIT was off, parallel workers were disabled,
custom plans were forced, and statement timeout was 60 seconds. The local
parity oracle used `MaxSteps=math.MaxInt`; that is not a PostgreSQL execution
budget. Raw logs, plans, IDs, and aggregate data remain outside Git in
`/private/tmp/hff-duration-evidence/` on the measurement host.

## Results

Build time is median ns/op with six-sample min–max ranges. Allocations are
cumulative `B/op` and `allocs/op`; SQL bytes are generated predicate length.
Allocation counts were unchanged in all 12 Build cases. `B/op` ranged from
effectively unchanged to 3.33% higher, and the candidate added 53 SQL bytes
per duration expression. Build timing was mixed and does not show a general
reduction.

| Format / workload | Baseline ns/op (range) → candidate ns/op (range) | B/op baseline → candidate | SQL bytes baseline → candidate |
|:--|--:|--:|--:|
| Native / StringControl | 20,837 (19,952–21,790) → 19,806 (19,445–21,226) | 24,097 → 24,098 | 339 → 339 |
| JSON / StringControl | 20,816 (19,119–23,709) → 20,758 (20,516–20,961) | 25,348 → 25,348 | 498 → 498 |
| JSONB / StringControl | 21,607 (20,770–22,541) → 20,852 (20,707–22,186) | 25,349 → 25,348 | 499 → 499 |
| Native / IntegerUnits | 25,643 (24,016–27,522) → 25,249 (24,762–25,624) | 41,449 → 42,475 | 2,112 → 2,165 |
| JSON / IntegerUnits | 27,048 (25,815–28,219) → 26,343 (25,992–27,234) | 42,380 → 43,792 | 2,273 → 2,326 |
| JSONB / IntegerUnits | 27,364 (26,489–28,216) → 26,018 (25,613–26,447) | 42,381 → 43,791 | 2,274 → 2,327 |
| Native / Repeated512 | 34,692 (32,400–37,301) → 34,914 (34,170–39,363) | 65,138 → 66,676 | 4,947 → 5,053 |
| JSON / Repeated512 | 37,026 (36,144–39,213) → 34,508 (33,923–35,055) | 66,068 → 67,605 | 5,108 → 5,214 |
| JSONB / Repeated512 | 36,080 (35,060–37,557) → 34,742 (33,868–34,990) | 66,069 → 67,606 | 5,109 → 5,215 |
| Native / Serialized512 | 27,433 (24,933–31,033) → 27,596 (27,131–29,966) | 45,940 → 47,094 | 3,056 → 3,109 |
| JSON / Serialized512 | 29,514 (28,747–30,400) → 27,692 (27,055–28,016) | 47,415 → 48,698 | 3,221 → 3,274 |
| JSONB / Serialized512 | 28,022 (26,316–34,067) → 27,630 (27,476–27,978) | 47,416 → 48,697 | 3,222 → 3,275 |

The execution table reports six-sample medians and min–max ranges in
milliseconds. Planning medians are also shown because execution alone does not
represent all measured server cost. All 51 workloads kept the same plan-node
count. Planning changes were mixed, from 17.1% lower to 18.6% higher across
the matrix, with no systematic reduction. The six-sample string-control
execution medians drifted from 11.1% lower to 8% higher across tiny timings;
their SQL was byte-identical.

| Format / rows / workload | Planning ms baseline → candidate | Execution ms baseline (range) → candidate (range) |
|:--|--:|--:|
| Native / 25 / StringControl | 0.0445 → 0.0465 | 0.0215 (0.018–0.024) → 0.021 (0.016–0.023) |
| JSON / 25 / StringControl | 0.058 → 0.0525 | 0.0295 (0.027–0.034) → 0.028 (0.023–0.050) |
| JSONB / 25 / StringControl | 0.050 → 0.046 | 0.0265 (0.021–0.031) → 0.0255 (0.024–0.028) |
| Native / 25 / CompoundUnits | 0.248 → 0.235 | 0.2765 (0.256–0.312) → 0.296 (0.259–0.333) |
| JSON / 25 / CompoundUnits | 0.250 → 0.267 | 0.289 (0.257–0.352) → 0.308 (0.261–0.327) |
| JSONB / 25 / CompoundUnits | 0.264 → 0.261 | 0.2695 (0.249–0.315) → 0.2985 (0.273–0.382) |
| Native / 25 / Fraction512 | 0.265 → 0.258 | 34.58 (34.23–36.83) → 9.809 (9.697–10.10) |
| JSON / 25 / Fraction512 | 0.305 → 0.329 | 34.25 (33.54–34.70) → 9.736 (9.457–10.11) |
| JSONB / 25 / Fraction512 | 0.303 → 0.278 | 34.40 (33.97–37.20) → 9.886 (9.526–10.15) |
| Native / 25 / Fraction4096 | 0.293 → 0.296 | 1,620 (1,599–1,698) → 93.75 (92.28–94.12) |
| JSON / 25 / Fraction4096 | 0.305 → 0.317 | 1,621 (1,608–1,704) → 92.72 (91.11–93.86) |
| JSONB / 25 / Fraction4096 | 0.299 → 0.305 | 1,621 (1,605–1,711) → 92.56 (91.57–92.94) |
| Native / 1 / Fraction20000 | 0.292 → 0.279 | 1,960 (1,935–1,966) → 48.86 (48.30–49.29) |
| JSON / 1 / Fraction20000 | 0.315 → 0.342 | 1,962 (1,944–2,017) → 49.24 (48.80–50.60) |
| JSONB / 1 / Fraction20000 | 0.300 → 0.314 | 1,952 (1,944–2,086) → 48.78 (48.25–49.57) |

For 25-row compound inputs, execution increased 6.6% to 10.8%. The two-row
compound samples were mixed, from 4.2% lower to 15.7% higher. For 512-digit
fractions, the 25-row samples were about 71–72% lower; for 4,096 digits, they
were about 94% lower. The single-row 20,000-digit samples were about 97.5%
lower. These are measurements of the tested inputs and local PostgreSQL
container, not a guarantee for other data or deployments.

## Correctness and tradeoffs

The candidate passed 8,930 duration comparisons, eight long-decimal boundary
cases, six SQL string escaping probes, and 87 shared conformance pairs. These
cover exact fractional summation, integral-nanosecond checks, int64 bounds,
invalid and null inputs, and parameter escaping. No parser grammar or
result-conversion behavior changed.

The byte-index path materially reduced execution time for tested long
fractional strings but added generated SQL and cumulative Build allocation.
The short compound-path regressions are retained above. The database timing is
from a single host/container profile with six samples per side; it is not a
production latency or service-level claim. Peak and retained memory were not
measured. PostgreSQL does not enforce the local parser's step budget; database
timeouts remain caller-owned.

## Decision and limits

The byte-index candidate is retained for its measured long-fraction results,
with the short compound overhead and Build tradeoff documented. Continue to
compare representative ordinary inputs alongside long fractions; do not
extrapolate the measured reduction to other PostgreSQL versions, plans, or
workloads.
