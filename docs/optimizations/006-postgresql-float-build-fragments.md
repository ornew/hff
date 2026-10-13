# 006. Precompute PostgreSQL Float SQL fragments

- **Status**: Superseded
- **Date**: 2026-10-13

> The native PostgreSQL Float model in [design 021](../design/021-backend-native-float-semantics.md)
> replaced the checked/unchecked Float parser templates measured here. The
> measurements below describe the earlier implementation only.

## Problem and mechanism

PostgreSQL Float lowering assembled the same checked and unchecked SQL
prefix/suffix fragments during each Build. The candidate precomputes those
immutable fragments at package initialization and inserts only the receiver
SQL while building an expression. Generated SQL, arguments, parameter counts,
and plans are unchanged. This targets Build allocation, not database execution.

## Applicability

| Workload | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Native JSON and metadata-backed JSONB scalar Float | Applied | Build reuses static checked/unchecked fragments. |
| Float guards and callbacks | Applied | Shared fragments avoid repeated fixed-string construction in nested SQL. |
| Non-Float lowering | Not applicable | This change only precomputes Float SQL fragments. |

## Reproduction

The baseline is [83e7ba7](https://github.com/ornew/hff/commit/83e7ba7556f5af4793c186037f4ef7714896d648).
The candidate source snapshot for `backends/sql/json_float.go` had SHA-256
`631197e9d5f3655d2c0c4ec0973511e09e9de182a466f482a6bf123ae7c8bb2e`.
The benchmark harness and fixture are linked at the baseline revision:
[Build cases](https://github.com/ornew/hff/blob/83e7ba7556f5af4793c186037f4ef7714896d648/backends/sql/metadata_bench_test.go)
and [execution cases](https://github.com/ornew/hff/blob/83e7ba7556f5af4793c186037f4ef7714896d648/backends/sql/metadata_execution_bench_test.go).
Build cases cover Native JSON and JSONB with numeric metadata across Scalar,
Guards, and Callback queries. Database cases cover those modes and shapes at
100 and 1,000 rows. Runs used Go 1.27.1 on Darwin 25.2.0/arm64, Apple M3 Max
with 64 GiB RAM and default `GOMAXPROCS=16`. PostgreSQL was 18.6 in a Debian
aarch64 container with image digest
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`;
the database directory was tmpfs, with no explicit CPU, memory, or cpuset
limits. JIT was off, parallel workers were disabled, plans were forced custom,
and statement timeout was 60 seconds. Two serialized baseline/candidate pairs
were run; tables below report the final three-sample pair.

Build command:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataBuild$/(NativeJSON|JSONBMetadata)/(Scalar|Guards|Callback)$' -benchmem \
  -benchtime=200ms -count=3
```

Database execution command (use separate plan directories for baseline and
candidate):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-float-plans \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataExecution$/(NativeJSON|JSONBMetadata)/(100Rows|1000Rows)/(Scalar|Guards|Callback)$' \
  -benchtime=1x -count=3
```

Raw logs, plans, SQL, and allocation profiles are outside Git in
`/private/tmp/hff-build-overhead-evidence/` on the measurement host.

## Results

Build timings are median nanoseconds per operation; parentheses show the
three-sample min–max. `B/op` is cumulative allocation, not retained or peak
memory. SQL bytes and parameter counts were identical before and after in all
six Build cases.

| Format / query | Baseline ns/op (min–max) | Candidate ns/op (min–max) | B/op | allocs/op | SQL bytes / params |
|:--|--:|--:|--:|--:|:--|
| Native JSON / Scalar | 24,737 (24,274–26,637) | 26,012 (24,024–27,570) | 59,883 → 58,024 | 248 → 243 | 5,051 / 2 |
| Native JSON / Guards | 30,206 (30,192–30,345) | 30,092 (29,973–31,069) | 58,230 → 56,370 | 433 → 428 | 5,280 / 4 |
| Native JSON / Callback | 40,873 (40,764–41,180) | 40,227 (39,948–40,249) | 118,730 → 115,012 | 564 → 554 | 10,346 / 4 |
| JSONB metadata / Scalar | 24,866 (24,782–24,870) | 24,557 (24,547–26,130) | 67,981 → 66,123 | 227 → 222 | 6,700 / 3 |
| JSONB metadata / Guards | 32,375 (32,261–32,624) | 32,248 (32,178–32,597) | 76,115 → 74,257 | 422 → 417 | 8,804 / 6 |
| JSONB metadata / Callback | 45,140 (43,721–47,498) | 42,554 (42,524–43,016) | 135,224 → 131,508 | 553 → 543 | 13,869 / 6 |

Allocation counts fell by five for Scalar and Guards and by ten for Callback in
both formats. The Native JSON Scalar timing was higher in the candidate sample;
Build timing differences are not treated as a general speed result.

The database execution runs used 12 format/shape/cardinality cases. Generated
SQL matched byte-for-byte in both baseline/candidate pairs, with the same
parameters and recursively identical sampled plan structures. The table shows
the planning and execution samples; their differences are noise for this
byte-identical SQL control and provide no database-speed claim for the
Build-only change. The full PostgreSQL integration suite was not run for this
candidate. Targeted Float/JSON tests, the repository check script, and
integration-tagged vet passed.

Planning and execution times are milliseconds per operation; parentheses show
the min–max of three samples. All cases had 79 matches at 100 rows and 793 at
1,000 rows. Plan-node counts are identical within each baseline/candidate pair.

| Format / rows / query | Planning ms/op: baseline → candidate (ranges) | Execution ms/op: baseline → candidate (ranges) | Plan nodes |
|:--|:--|:--|--:|
| Native JSON / 100 / Scalar | 0.272 (0.266–0.274) → 0.285 (0.270–0.378) | 0.624 (0.614–0.745) → 0.652 (0.592–0.725) | 27 |
| Native JSON / 100 / Guards | 0.359 (0.345–0.377) → 0.382 (0.361–0.471) | 1.268 (1.252–1.282) → 1.286 (1.216–1.328) | 41 |
| Native JSON / 100 / Callback | 0.549 (0.546–0.593) → 0.513 (0.511–0.541) | 2.486 (2.366–2.536) → 2.326 (2.324–2.447) | 66 |
| Native JSON / 1,000 / Scalar | 0.265 (0.252–0.270) → 0.277 (0.254–0.284) | 5.335 (5.277–5.650) → 5.583 (5.554–6.018) | 27 |
| Native JSON / 1,000 / Guards | 0.369 (0.366–0.374) → 0.390 (0.350–0.390) | 12.12 (12.08–12.47) → 12.58 (12.26–12.74) | 41 |
| Native JSON / 1,000 / Callback | 0.509 (0.505–0.592) → 0.578 (0.569–0.589) | 23.14 (22.89–23.24) → 23.50 (23.41–23.52) | 66 |
| JSONB metadata / 100 / Scalar | 0.402 (0.368–0.404) → 0.381 (0.356–0.444) | 0.609 (0.565–0.716) → 0.580 (0.575–0.638) | 41 |
| JSONB metadata / 100 / Guards | 0.707 (0.688–0.768) → 0.696 (0.672–0.712) | 2.170 (2.055–2.228) → 2.227 (2.028–2.247) | 73 |
| JSONB metadata / 100 / Callback | 0.873 (0.821–0.889) → 0.847 (0.839–0.890) | 3.174 (3.157–3.254) → 3.181 (3.129–3.235) | 98 |
| JSONB metadata / 1,000 / Scalar | 0.391 (0.381–0.413) → 0.417 (0.412–0.428) | 5.417 (5.270–5.471) → 5.330 (5.241–5.465) | 41 |
| JSONB metadata / 1,000 / Guards | 0.655 (0.614–0.671) → 0.646 (0.634–0.649) | 20.14 (19.89–20.46) → 19.80 (19.77–19.85) | 73 |
| JSONB metadata / 1,000 / Callback | 0.902 (0.888–0.924) → 0.905 (0.861–0.953) | 31.18 (31.12–32.09) → 31.14 (31.12–31.17) | 98 |

## Correctness and tradeoffs

The generated query text and argument order remain identical, so the change
does not alter PostgreSQL behavior, planning, or execution. Fixed fragments
are retained as package-level immutable data; initialization cost and retained
memory were not measured. The focused allocation profile attributed about 6.8% of flat
allocation space to `jsonFloatValue` in the profiled workload, while expression
and string assembly dominate overall Build allocation.

## Decision and limits

The change is retained for its lower allocation counts in the measured Build
matrix without SQL growth. The results do not establish reduced retained
memory, lower database time, or a general latency improvement. Revisit only
with a workload that measures the relevant memory or Build behavior.
