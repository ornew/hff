# 009. Reduce PostgreSQL canonical JSON lowering work

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

Canonical serialization of dynamic JSON values used a recursive token walk
that expanded scalar nodes as though they could have children. Float tokens
also decoded the same lexical number in more than one expression. The
candidate keeps scalar/null rows as token sources but filters them out of the
recursive child-expansion term. It projects the decoded Float value once in the
local Float-token `CASE`/`LATERAL` scope before formatting canonical Float text.
Array order, object key sorting, numeric subtype and signed-zero output, null,
and no-value behavior remain unchanged.

## Applicability

| Serialization shape | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Dynamic scalar, nested, wide, or deep JSON values | Applied | The recursive token walk skips scalar/null child expansion and reuses the projected Float value. |
| Identity `map()` outputs serialized afterward | Applied | The resulting list uses the same serializer lowering. |
| Typed String and typed String-list controls | Not applicable | These paths do not use the recursive dynamic token walk; their SQL remains byte-identical. |
| JSONB input with required numeric metadata | Applied | Existing type/text restoration requirements remain in force before serialization. |

## Reproduction

The baseline is [96286b2](https://github.com/ornew/hff/commit/96286b2d3639664ec4829c6d1cc7d9364cfc1cdc). The candidate `backends/sql/json_canonical.go` source snapshot has SHA-256 `5031dda6326eb33e0631f55e50ba996a5f71c2e2a3f21a964df398e4f983d9b5`. The same harness was applied to baseline and candidate:

- [Build benchmark](https://github.com/ornew/hff/blob/main/backends/sql/serialization_bench_test.go), SHA-256 `76fcb9398b09ad4310e1e4eafe0281ed21e2ebaf794567f649232240c75b809c`
- [Execution benchmark](https://github.com/ornew/hff/blob/main/backends/sql/serialization_execution_bench_test.go), SHA-256 `11fdd635962bffe4ccee4ab791478ffb002f49a66970e36d286ff18b19c96484`

The seven shapes were `TypedStringControl`, `TypedListControl`, `AnyScalar`,
`AnyNested`, `AnyWide`, `AnyDeep`, and `Callback`. The first two serialize a
schema-known string and string list. `AnyScalar` cycles through null, a large
integer, Float values including negative zero, a string, and a boolean.
`AnyNested` contains an object with a list of numeric, null, and scalar values.
`AnyWide` has 64 object keys and 16 Float leaves; `AnyDeep` nests 16 alternating
arrays and objects. `Callback` serializes the results of an identity map over a
list. All dynamic cases use `value:json=expected`, except the callback case,
which uses `value:map(x=>x):json=expected`. Both native JSON and JSONB with
numeric metadata were measured; database fixtures had 50 and 250 rows.

The baseline and candidate were run in serialized three-sample pairs. Build
sub-benchmarks ran for 200 ms; each database sample executed one query after
checking result IDs against local HFF. The final tables use only the final
baseline/candidate pair. Earlier baseline, prune-only, and projected-Float
experiments are exploratory and are not mixed into these results.

The host was Darwin 25.2.0, arm64, Apple M3 Max with 64 GiB RAM. Go was 1.27.1
with default `GOMAXPROCS=16`. PostgreSQL was 18.6 in a Debian aarch64 container
using image digest `sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`.
There were no explicit CPU, memory, or cpuset limits. JIT was off, parallel
workers were disabled, custom plans were forced, statement timeout was 60
seconds, and the database directory used tmpfs. No retained-heap measurement
was made.

Build command:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLSerializationBuild$/(json|jsonb)/(TypedStringControl|TypedListControl|AnyScalar|AnyNested|AnyWide|AnyDeep|Callback)$' \
  -benchmem -benchtime=200ms -count=3
```

Database execution command (use separate plan directories for baseline and
candidate):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-serialization-plans \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLSerializationExecution$/(json|jsonb)/(50Rows|250Rows)/(TypedStringControl|TypedListControl|AnyScalar|AnyNested|AnyWide|AnyDeep|Callback)$' \
  -benchtime=1x -count=3
```

Raw logs, SQL, plans, and IDs remain outside Git in
`/private/tmp/hff-serialization-evidence/` on the measurement host.

## Results

Build time is median ns/op with the three-sample min–max. Allocation is
cumulative `B/op`, not peak or retained memory. SQL bytes are the generated
predicate length.

| Format / workload | Baseline ns/op (range) | Candidate ns/op (range) | B/op baseline → candidate | allocs/op baseline → candidate | SQL bytes baseline → candidate |
|:--|--:|--:|--:|--:|--:|
| JSON / TypedStringControl | 20,228 (18,855–21,076) | 18,768 (18,742–21,091) | 26,021 → 26,021 | 185 → 185 | 554 → 554 |
| JSON / TypedListControl | 19,483 (19,428–19,606) | 19,795 (19,730–19,835) | 29,580 → 29,581 | 197 → 197 | 973 → 973 |
| JSON / AnyScalar | 46,685 (46,098–46,741) | 32,056 (31,906–32,228) | 273,857 → 125,408 | 252 → 255 | 22,856 → 9,053 |
| JSON / AnyNested | 47,227 (46,900–49,143) | 32,381 (31,759–32,534) | 273,860 → 125,408 | 252 → 255 | 22,856 → 9,053 |
| JSON / AnyWide | 47,389 (47,212–48,168) | 32,053 (32,032–32,139) | 273,863 → 125,409 | 252 → 255 | 22,856 → 9,053 |
| JSON / AnyDeep | 47,136 (47,043–47,158) | 32,059 (31,969–32,352) | 273,866 → 125,409 | 252 → 255 | 22,856 → 9,053 |
| JSON / Callback | 53,993 (53,797–54,015) | 37,598 (37,422–37,651) | 305,322 → 143,263 | 359 → 361 | 24,031 → 10,217 |
| JSONB metadata / TypedStringControl | 19,833 (19,708–19,894) | 20,065 (20,047–20,067) | 33,178 → 33,178 | 195 → 195 | 2,248 → 2,248 |
| JSONB metadata / TypedListControl | 20,842 (20,692–20,977) | 21,147 (21,074–21,202) | 36,560 → 36,560 | 207 → 207 | 2,667 → 2,667 |
| JSONB metadata / AnyScalar | 48,645 (48,512–48,805) | 33,458 (33,428–33,647) | 277,558 → 131,905 | 262 → 265 | 24,570 → 10,760 |
| JSONB metadata / AnyNested | 48,495 (48,232–48,731) | 33,712 (33,600–33,753) | 277,563 → 131,909 | 262 → 265 | 24,570 → 10,760 |
| JSONB metadata / AnyWide | 48,343 (48,213–48,735) | 38,969 (35,816–41,935) | 277,567 → 131,912 | 262 → 265 | 24,570 → 10,760 |
| JSONB metadata / AnyDeep | 48,903 (48,042–49,271) | 43,756 (39,471–68,103) | 277,564 → 131,907 | 262 → 265 | 24,570 → 10,760 |
| JSONB metadata / Callback | 56,119 (55,949–57,010) | 40,202 (39,413–44,376) | 314,466 → 151,081 | 370 → 372 | 25,737 → 11,923 |

Database timings are median milliseconds from three one-execution samples,
with min–max ranges shown. Plan-node counts and SQL bytes are baseline →
candidate; matched rows are the unchanged expected count. The controls' SQL
and plan-node counts are unchanged.

| Format / rows / workload | Planning ms baseline (range) → candidate (range) | Execution ms baseline (range) → candidate (range) | Nodes baseline → candidate | SQL bytes baseline → candidate | Matched rows |
|:--|--:|--:|--:|--:|--:|
| JSON / 50 / TypedStringControl | 0.063 (0.050–0.071) → 0.065 (0.059–0.068) | 0.055 (0.055–0.059) → 0.061 (0.055–0.063) | 10 → 10 | 554 → 554 | 42 |
| JSON / 50 / TypedListControl | 0.100 (0.084–0.148) → 0.092 (0.087–0.099) | 0.138 (0.125–0.148) → 0.124 (0.115–0.133) | 12 → 12 | 973 → 973 | 42 |
| JSON / 50 / AnyScalar | 1.473 (1.313–1.661) → 0.707 (0.693–0.930) | 0.852 (0.839–0.943) → 0.620 (0.619–0.734) | 117 → 64 | 22,856 → 9,053 | 42 |
| JSON / 50 / AnyNested | 1.413 (1.370–1.734) → 0.797 (0.770–0.869) | 4.018 (3.663–4.113) → 3.160 (3.051–3.362) | 117 → 64 | 22,856 → 9,053 | 42 |
| JSON / 50 / AnyWide | 1.396 (1.394–1.682) → 0.861 (0.848–0.879) | 43.760 (43.670–45.280) → 34.880 (34.850–35.170) | 117 → 64 | 22,856 → 9,053 | 42 |
| JSON / 50 / AnyDeep | 1.685 (1.448–2.265) → 0.783 (0.759–0.818) | 8.067 (7.794–11.320) → 6.961 (6.891–7.199) | 117 → 64 | 22,856 → 9,053 | 42 |
| JSON / 50 / Callback | 1.486 (1.405–1.904) → 0.984 (0.978–1.137) | 4.560 (4.311–4.742) → 3.549 (3.529–3.662) | 133 → 80 | 24,031 → 10,217 | 42 |
| JSON / 250 / TypedStringControl | 0.070 (0.055–0.077) → 0.067 (0.062–0.071) | 0.225 (0.222–0.243) → 0.225 (0.215–0.363) | 10 → 10 | 554 → 554 | 214 |
| JSON / 250 / TypedListControl | 0.098 (0.092–0.109) → 0.078 (0.073–0.099) | 0.476 (0.468–0.496) → 0.545 (0.516–0.558) | 12 → 12 | 973 → 973 | 214 |
| JSON / 250 / AnyScalar | 1.396 (1.283–1.497) → 0.706 (0.696–0.775) | 3.442 (3.359–3.603) → 2.691 (2.665–2.750) | 117 → 64 | 22,856 → 9,053 | 214 |
| JSON / 250 / AnyNested | 1.330 (1.326–1.467) → 0.791 (0.777–0.904) | 17.880 (17.870–18.300) → 14.730 (14.610–15.120) | 117 → 64 | 22,856 → 9,053 | 214 |
| JSON / 250 / AnyWide | 1.381 (1.375–1.457) → 0.886 (0.836–0.980) | 219.100 (216.800–220.000) → 172.300 (172.200–182.400) | 117 → 64 | 22,856 → 9,053 | 214 |
| JSON / 250 / AnyDeep | 1.421 (1.418–1.466) → 0.886 (0.840–0.957) | 40.330 (39.210–41.160) → 33.700 (32.690–33.880) | 117 → 64 | 22,856 → 9,053 | 214 |
| JSON / 250 / Callback | 1.511 (1.452–1.545) → 1.015 (0.955–1.050) | 20.880 (20.560–21.510) → 16.530 (16.480–16.690) | 133 → 80 | 24,031 → 10,217 | 214 |
| JSONB metadata / 50 / TypedStringControl | 0.236 (0.186–0.351) → 0.243 (0.212–0.247) | 0.176 (0.169–0.179) → 0.203 (0.166–0.268) | 26 → 26 | 2,248 → 2,248 | 42 |
| JSONB metadata / 50 / TypedListControl | 0.300 (0.294–0.321) → 0.263 (0.255–0.283) | 0.502 (0.453–0.515) → 0.498 (0.425–0.521) | 28 → 28 | 2,667 → 2,667 | 42 |
| JSONB metadata / 50 / AnyScalar | 1.541 (1.441–1.782) → 0.915 (0.891–0.979) | 1.067 (0.980–1.129) → 0.847 (0.821–0.966) | 133 → 80 | 24,570 → 10,760 | 42 |
| JSONB metadata / 50 / AnyNested | 1.410 (1.406–1.527) → 0.883 (0.834–0.943) | 4.676 (4.555–4.736) → 3.992 (3.958–4.191) | 133 → 80 | 24,570 → 10,760 | 42 |
| JSONB metadata / 50 / AnyWide | 1.555 (1.499–1.568) → 0.911 (0.900–0.971) | 51.190 (50.630–52.290) → 42.530 (41.720–42.760) | 133 → 80 | 24,570 → 10,760 | 42 |
| JSONB metadata / 50 / AnyDeep | 1.516 (1.458–1.527) → 1.079 (0.988–1.079) | 12.710 (12.310–12.840) → 11.470 (11.260–11.540) | 133 → 80 | 24,570 → 10,760 | 42 |
| JSONB metadata / 50 / Callback | 1.526 (1.503–1.692) → 1.083 (1.061–1.183) | 5.044 (4.922–5.159) → 4.235 (4.131–4.236) | 149 → 96 | 25,737 → 11,923 | 42 |
| JSONB metadata / 250 / TypedStringControl | 0.205 (0.195–0.247) → 0.243 (0.205–0.255) | 0.686 (0.656–0.689) → 0.691 (0.690–0.692) | 26 → 26 | 2,248 → 2,248 | 214 |
| JSONB metadata / 250 / TypedListControl | 0.277 (0.257–0.290) → 0.272 (0.241–0.328) | 2.029 (2.007–2.165) → 2.141 (2.009–2.160) | 28 → 28 | 2,667 → 2,667 | 214 |
| JSONB metadata / 250 / AnyScalar | 1.543 (1.469–1.718) → 0.945 (0.826–1.050) | 4.042 (4.034–4.132) → 3.231 (3.090–3.251) | 133 → 80 | 24,570 → 10,760 | 214 |
| JSONB metadata / 250 / AnyNested | 1.510 (1.474–1.535) → 0.928 (0.875–1.031) | 22.010 (21.180–22.530) → 18.430 (18.220–18.480) | 133 → 80 | 24,570 → 10,760 | 214 |
| JSONB metadata / 250 / AnyWide | 1.561 (1.551–1.600) → 0.983 (0.980–1.045) | 254.100 (254.100–256.300) → 209.400 (207.900–217.300) | 133 → 80 | 24,570 → 10,760 | 214 |
| JSONB metadata / 250 / AnyDeep | 1.624 (1.595–1.695) → 1.023 (0.966–1.065) | 61.770 (61.160–61.860) → 54.690 (54.500–54.910) | 133 → 80 | 24,570 → 10,760 | 214 |
| JSONB metadata / 250 / Callback | 1.611 (1.582–1.635) → 1.110 (1.109–1.113) | 24.080 (23.950–24.390) → 19.990 (19.980–20.130) | 149 → 96 | 25,737 → 11,923 | 214 |

The eight typed controls retain identical SQL; their timings vary, including a
JSON `TypedListControl` execution increase from 0.476 ms to 0.545 ms at 250
rows. All ten dynamic 250-row cases measured lower candidate execution times.
For example, `AnyWide` changed from 219.1 ms to 172.3 ms for JSON and from
254.1 ms to 209.4 ms for metadata-backed JSONB. These are measurements for the
listed fixtures, not a general latency or memory guarantee.

## Correctness and tradeoffs

The canonical serialization differential suite passed 324 comparisons for
each candidate implementation across native JSON and metadata-backed JSONB.
It covered scalar, nested, deep, identity-map output, whitespace and escapes,
Float subtype, and negative-zero cases. The 64-key wide shape is a benchmark
workload, not part of that 324-comparison suite. A separate 64-case guard
matrix covered null, type, list, and path guards across JSON and JSONB. The
final change preserved array order, canonical object-key order, numeric
representation, null-token treatment, and the caller's JSONB metadata
contract. No full PostgreSQL package integration suite was rerun for this
isolated token-expression change; the database benchmark checked SQL IDs
against local results for every sample. No peak or retained-memory measurement
was made.

Build `B/op` fell for dynamic workloads while `allocs/op` increased by two or
three; these are separate cumulative allocation measures. The 1-second
CPU/allocation profiles for native-JSON `AnyNested` did not identify a
compiler-specific CPU hotspot: sampled CPU time was dominated by runtime and
GC waits. Allocation profiles locate cumulative allocation in Float text,
canonical JSON, and expression construction. Profile totals are not compared
because the runs completed different iteration counts.

The focused profile command was:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLSerializationBuild$/json$/AnyNested$' \
  -benchtime=1s -count=1 -cpuprofile=<path> -memprofile=<path> \
  -o=<binary-outside-repository>
```

The canonical differential test and project checks passed:

```sh
TMPDIR=/private/tmp HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 \
  go test -tags integration ./backends/sql \
  -run '^TestPostgreSQL(DynamicCanonicalJSON|CanonicalSerializationGuards)$' \
  -count=1 -v
TMPDIR=/private/tmp HFF_WASM_NODE=<node-path> GOTOOLCHAIN=go1.27.1 sh hack/check.sh
TMPDIR=/private/tmp GOTOOLCHAIN=go1.27.1 go vet -tags integration ./backends/sql
```

The focused tests passed 324 canonical comparisons and 64 guard cases in
6.479 seconds. The project check covered formatting, race tests, vet,
generation, examples, WASM tests, and site checks. No full PostgreSQL package
integration run was performed for this isolated change.

## Decision and limits

Retain scalar-leaf child-expansion pruning and one-time local Float-token
decoding. The candidate reduces generated SQL and plan nodes for the dynamic
token-CTE workloads; controls do not use that CTE and keep byte-identical SQL.
The results do not establish faster PostgreSQL execution for every query,
physical evaluation order, or compliance with local runtime budgets.
