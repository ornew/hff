# 011. Reduce dynamic PostgreSQL JSON sort lowering work

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

Dynamic PostgreSQL sorting reconstructed a comparison document even when a
native JSON `Any` list already retained the element representations needed by
the sort. Float sort keys also repeated exact numeric decomposition for zero
values. The candidate array-gates and consumes the original native JSON list
for `Any` elements. For recoverable dynamic values, it projects each decoded
Float locally and uses numeric zero directly as the exact sort key, avoiding
unnecessary decomposition for zero; nonzero values still use the shared
`floatNumeric` lowering. JSONB continues to normalize through
the comparison-document path when needed to restore numeric subtypes. The
schema-resolved homogeneous fast paths and global `floatNumeric` implementation
are unchanged.

These changes were measured together; the results do not isolate the effect of
either mechanism. Sorting still rejects mixed runtime families and null or
non-orderable elements as ordinary null. Stable ordering and the original
representations of equal values are preserved when those representations are
recoverable. JSONB type metadata remains necessary when normalization erased
an integer/float distinction; a schema-known Float type alone cannot restore a
negative-zero sign lost by JSONB.

## Applicability

| Workload or profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Native JSON `Any` list | Applied | Uses the already-preserved JSON array after the array gate instead of rebuilding a comparison document. |
| JSONB `Any` or list-union values | Partial | Exact Float-key projection applies; comparison-document normalization remains where subtype restoration is required. |
| Repeated dynamic sorts | Applied | Both sort operations use the same candidate lowering; the repeated-sort workload is measured separately. |
| Nested or mixed invalid dynamic lists | Partial | The candidate keeps the category and applicability checks; it changes the measured SQL construction around them. |
| Schema-resolved homogeneous Int/Float controls | Not applied | The existing sequence fast path remains byte-identical. |
| Local evaluation, arbitrary PostgreSQL plans, and production workloads | Not applicable | The measurements cover PostgreSQL Build and a controlled local PostgreSQL fixture only. |

## Reproduction

The baseline is [290e7f6](https://github.com/ornew/hff/commit/290e7f61035fdcac7d4c66eae27ab18335334a94). The candidate source snapshot for `backends/sql/json_sort_dynamic.go` has SHA-256 `cb3e9bc4b514426996e6474db8614115042c985ec8fac299135146c127c3911f`. The same benchmark harness was applied to both revisions:

- [Build benchmark](https://github.com/ornew/hff/blob/main/backends/sql/sort_bench_test.go), SHA-256 `0096237bedf8efce2b67ccb14d30d1e3b0c793afcb688634ea56b986c97888b6`
- [PostgreSQL execution benchmark](https://github.com/ornew/hff/blob/main/backends/sql/sort_execution_bench_test.go), SHA-256 `aa03b8b6e48e4d7f905c8a2533f5ea7519d67744743f43d7ad58609f9c64da0f`

The Build matrix crosses native JSON and JSONB with `TypedIntControl`,
`TypedFloatControl`, `AnyNumbers`, `AnyStrings`, `AnyInvalidNested`,
`AnyInvalidMixed`, and `RepeatedSort`. Execution uses those workloads with
25 or 100 rows and 16 or 64 elements per list. Dynamic fixtures include
mixed int64/float64 values, strings, signed zero, nested non-orderable values,
invalid mixed families, and null rows. Each database sample first checks
matching IDs against local HFF before measuring the `EXPLAIN ANALYZE` query;
fixture creation, transport, and validation are not included in the reported
execution time.

The host was Darwin 25.2.0, arm64, Apple M3 Max with 64 GiB RAM. Go was 1.27.1
with default `GOMAXPROCS=16`. PostgreSQL was 18.6 in a Debian aarch64 container
using image digest
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`.
The container had no explicit CPU, memory, or cpuset limits, and its data
directory used tmpfs. The runs used no Docker network, disabled JIT and
parallel workers, forced custom plans, and set a 60-second statement timeout.

The final aggregate combines two serialized three-sample rounds in the order
baseline, candidate, baseline repeat, candidate repeat (six samples per side).
Raw logs, plans, and per-sample results remain outside Git in
`/private/tmp/hff-sort-evidence/` on the measurement host.

Build command:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLSortBuild$/(json|jsonb)/(TypedIntControl|TypedFloatControl|AnyNumbers|AnyStrings|AnyInvalidNested|AnyInvalidMixed|RepeatedSort)$' \
  -benchmem -benchtime=200ms -count=3
```

PostgreSQL execution command (use separate plan directories for each revision
and repeat):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-sort-plans-baseline \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLSortExecution$/(json|jsonb)/(25Rows|100Rows)/(16Elements|64Elements)/(TypedIntControl|TypedFloatControl|AnyNumbers|AnyStrings|AnyInvalidNested|AnyInvalidMixed|RepeatedSort)$' \
  -benchtime=1x -count=3
```

## Results

Build time is median ns/op with min–max ranges across six samples. `B/op` is
cumulative allocation, not retained or peak memory. SQL bytes and parameter
counts describe the generated predicate.

| Format / workload | Build ns/op baseline (range) → candidate (range) | B/op baseline → candidate | allocs/op baseline → candidate | Parameters baseline → candidate | SQL bytes baseline → candidate |
|:--|--:|--:|--:|--:|--:|
| json / TypedIntControl | 23,374 (21,537–25,759) → 22,386.5 (21,489–26,373) | 32,250 → 32,250 | 211 → 211 | 2 → 2 | 1,415 → 1,415 |
| json / TypedFloatControl | 28,747 (26,710–31,982) → 26,755.5 (26,570–26,952) | 81,734.5 → 81,734 | 221 → 221 | 2 → 2 | 7,542 → 7,542 |
| json / AnyNumbers | 43,421 (40,901–46,615) → 41,048.5 (40,937–41,173) | 197,195 → 198,418 | 250 → 238 | 2 → 2 | 31,365 → 30,816 |
| json / AnyStrings | 30,261 (28,672–30,877) → 28,109.5 (27,935–29,407) | 88,874.5 → 87,009.5 | 237 → 224 | 2 → 2 | 7,253 → 6,704 |
| json / AnyInvalidNested | 27,686.5 (26,541–28,897) → 26,372 (26,009–26,685) | 82,351.5 → 82,808 | 202 → 190 | 0 → 0 | 6,495 → 5,947 |
| json / AnyInvalidMixed | 26,774 (26,717–28,376) → 26,259 (26,114–26,746) | 82,353 → 82,807.5 | 202 → 190 | 0 → 0 | 6,495 → 5,947 |
| json / RepeatedSort | 51,979 (51,627–56,458) → 50,883 (50,712–51,297) | 261,623 → 263,421 | 337 → 315 | 2 → 2 | 37,341 → 36,451 |
| jsonb / TypedIntControl | 23,082 (22,702–23,527) → 22,831 (22,598–22,912) | 39,233 → 39,233 | 221 → 221 | 2 → 2 | 3,111 → 3,111 |
| jsonb / TypedFloatControl | 28,549 (28,070–29,354) → 28,074 (27,742–28,536) | 87,969.5 → 87,967.5 | 231 → 231 | 2 → 2 | 9,243 → 9,243 |
| jsonb / AnyNumbers | 44,454 (43,905–44,804) → 45,184 (42,242–54,916) | 218,293.5 → 202,243 | 260 → 248 | 2 → 2 | 33,094 → 32,533 |
| jsonb / AnyStrings | 30,192 (30,081–30,608) → 30,188.5 (29,605–31,441) | 96,107 → 96,223 | 247 → 234 | 2 → 2 | 8,982 → 8,421 |
| jsonb / AnyInvalidNested | 28,492 (28,453–28,898) → 27,942 (27,637–28,452) | 91,650 → 90,719.5 | 212 → 200 | 0 → 0 | 8,224 → 7,663 |
| jsonb / AnyInvalidMixed | 28,856 (28,514–29,081) → 27,914.5 (27,518–28,134) | 91,649.5 → 90,720 | 212 → 200 | 0 → 0 | 8,224 → 7,663 |
| jsonb / RepeatedSort | 52,669 (52,239–54,123) → 52,740.5 (52,473–53,774) | 266,288 → 267,244.5 | 348 → 325 | 2 → 2 | 39,070 → 38,167 |

Database planning and execution are median milliseconds with min–max ranges
across six single-execution samples. Plan-node counts, SQL bytes, and expected
matched rows are baseline → candidate. The 16 control SQL scripts were
byte-identical across the four runs. Across all 16 controls, execution medians
ranged from 3.9% lower to 10.5% higher; at 100 rows and 64 elements, control
drift was about 1–4%. These six-sample measurements come from one environment
without independent replication, so small differences should not be read as
stable changes.

| Format / rows / elements / workload | Planning ms baseline (range) → candidate (range) | Execution ms baseline (range) → candidate (range) | Plan nodes baseline → candidate | SQL bytes baseline → candidate | Matched rows |
|:--|--:|--:|--:|--:|--:|
| json / 25Rows / 16Elements / TypedIntControl | 0.162 (0.124–0.253) → 0.149 (0.124–0.185) | 0.387 (0.369–0.471) → 0.427 (0.392–0.474) | 19 → 19 | 1,415 → 1,415 | 20 |
| json / 25Rows / 16Elements / TypedFloatControl | 0.416 (0.348–0.47) → 0.431 (0.394–0.508) | 0.95 (0.834–1.026) → 0.941 (0.91–1.335) | 45 → 45 | 7,542 → 7,542 | 20 |
| json / 25Rows / 16Elements / AnyNumbers | 1.048 (0.962–1.098) → 1.076 (0.991–1.469) | 1.692 (1.598–2.398) → 1.511 (1.396–1.644) | 135 → 133 | 31,365 → 30,816 | 20 |
| json / 25Rows / 16Elements / AnyStrings | 0.444 (0.387–0.502) → 0.438 (0.379–0.458) | 0.519 (0.472–0.586) → 0.463 (0.437–0.527) | 43 → 41 | 7,253 → 6,704 | 20 |
| json / 25Rows / 16Elements / AnyInvalidNested | 0.384 (0.351–0.393) → 0.376 (0.35–0.574) | 0.173 (0.164–0.22) → 0.113 (0.102–0.122) | 37 → 35 | 6,495 → 5,947 | 22 |
| json / 25Rows / 16Elements / AnyInvalidMixed | 0.386 (0.342–0.493) → 0.359 (0.32–0.4) | 0.256 (0.239–0.313) → 0.237 (0.208–0.252) | 37 → 35 | 6,495 → 5,947 | 22 |
| json / 25Rows / 16Elements / RepeatedSort | 1.255 (1.111–1.366) → 1.288 (1.144–1.742) | 3.236 (3.039–3.294) → 2.868 (2.621–3.249) | 166 → 162 | 37,341 → 36,451 | 20 |
| json / 100Rows / 16Elements / TypedIntControl | 0.162 (0.157–0.179) → 0.157 (0.133–0.206) | 1.356 (1.262–1.437) → 1.349 (1.309–1.366) | 19 → 19 | 1,415 → 1,415 | 80 |
| json / 100Rows / 16Elements / TypedFloatControl | 0.41 (0.393–0.43) → 0.398 (0.365–0.46) | 3.51 (3.423–3.746) → 3.559 (3.441–3.645) | 45 → 45 | 7,542 → 7,542 | 80 |
| json / 100Rows / 16Elements / AnyNumbers | 1.031 (0.975–1.145) → 1.054 (0.975–1.101) | 6.471 (6.218–6.665) → 5.646 (5.267–5.914) | 135 → 133 | 31,365 → 30,816 | 80 |
| json / 100Rows / 16Elements / AnyStrings | 0.438 (0.378–0.479) → 0.423 (0.369–0.45) | 2.015 (1.852–2.306) → 1.647 (1.605–1.784) | 43 → 41 | 7,253 → 6,704 | 80 |
| json / 100Rows / 16Elements / AnyInvalidNested | 0.379 (0.364–0.4) → 0.374 (0.322–0.417) | 0.59 (0.509–0.688) → 0.275 (0.244–0.306) | 37 → 35 | 6,495 → 5,947 | 86 |
| json / 100Rows / 16Elements / AnyInvalidMixed | 0.376 (0.368–0.429) → 0.368 (0.311–0.442) | 0.893 (0.869–0.912) → 0.679 (0.627–0.721) | 37 → 35 | 6,495 → 5,947 | 86 |
| json / 100Rows / 16Elements / RepeatedSort | 1.322 (1.192–1.364) → 1.313 (1.27–1.434) | 12.09 (11.76–12.29) → 10.595 (10.19–11.02) | 166 → 162 | 37,341 → 36,451 | 80 |
| json / 25Rows / 64Elements / TypedIntControl | 0.14 (0.117–0.17) → 0.155 (0.133–0.232) | 1.094 (1.003–1.228) → 1.052 (1.011–1.13) | 19 → 19 | 1,415 → 1,415 | 20 |
| json / 25Rows / 64Elements / TypedFloatControl | 0.421 (0.366–0.539) → 0.377 (0.356–0.426) | 3.14 (2.837–3.534) → 3.072 (2.89–3.219) | 45 → 45 | 7,542 → 7,542 | 20 |
| json / 25Rows / 64Elements / AnyNumbers | 1.051 (1.003–1.456) → 1.093 (1.028–1.165) | 5.604 (5.363–5.75) → 4.902 (4.652–5.41) | 135 → 133 | 31,365 → 30,816 | 20 |
| json / 25Rows / 64Elements / AnyStrings | 0.418 (0.391–0.489) → 0.427 (0.397–0.448) | 1.582 (1.488–1.752) → 1.434 (1.34–1.478) | 43 → 41 | 7,253 → 6,704 | 20 |
| json / 25Rows / 64Elements / AnyInvalidNested | 0.423 (0.338–0.479) → 0.378 (0.334–0.395) | 0.553 (0.509–0.631) → 0.221 (0.217–0.272) | 37 → 35 | 6,495 → 5,947 | 22 |
| json / 25Rows / 64Elements / AnyInvalidMixed | 0.368 (0.34–0.39) → 0.374 (0.367–0.38) | 0.679 (0.627–0.713) → 0.523 (0.484–0.551) | 37 → 35 | 6,495 → 5,947 | 22 |
| json / 25Rows / 64Elements / RepeatedSort | 1.204 (1.177–1.514) → 1.268 (1.2–1.523) | 11.145 (10.52–12.04) → 9.639 (9.4–10.01) | 166 → 162 | 37,341 → 36,451 | 20 |
| json / 100Rows / 64Elements / TypedIntControl | 0.161 (0.129–0.17) → 0.176 (0.16–0.219) | 4.223 (4.026–4.57) → 4.278 (4.07–4.414) | 19 → 19 | 1,415 → 1,415 | 80 |
| json / 100Rows / 64Elements / TypedFloatControl | 0.441 (0.399–0.483) → 0.426 (0.366–0.463) | 11.78 (11.34–12.52) → 12.04 (11.86–13.76) | 45 → 45 | 7,542 → 7,542 | 80 |
| json / 100Rows / 64Elements / AnyNumbers | 1.158 (0.996–1.41) → 1.1 (1.034–1.423) | 22.62 (21.9–23.03) → 19.47 (19.05–19.84) | 135 → 133 | 31,365 → 30,816 | 80 |
| json / 100Rows / 64Elements / AnyStrings | 0.455 (0.398–0.502) → 0.44 (0.392–0.469) | 6.225 (5.96–6.473) → 5.466 (5.352–5.884) | 43 → 41 | 7,253 → 6,704 | 80 |
| json / 100Rows / 64Elements / AnyInvalidNested | 0.414 (0.353–0.477) → 0.366 (0.339–0.469) | 1.971 (1.888–2.098) → 0.745 (0.697–0.798) | 37 → 35 | 6,495 → 5,947 | 86 |
| json / 100Rows / 64Elements / AnyInvalidMixed | 0.405 (0.379–0.474) → 0.378 (0.348–0.431) | 2.649 (2.545–2.796) → 1.9 (1.81–1.93) | 37 → 35 | 6,495 → 5,947 | 86 |
| json / 100Rows / 64Elements / RepeatedSort | 1.374 (1.275–1.802) → 1.267 (1.186–1.31) | 43.66 (41.79–46.42) → 38.065 (37.91–38.47) | 166 → 162 | 37,341 → 36,451 | 80 |
| jsonb / 25Rows / 16Elements / TypedIntControl | 0.325 (0.288–0.346) → 0.328 (0.289–0.393) | 0.986 (0.919–1.015) → 0.948 (0.907–1.022) | 35 → 35 | 3,111 → 3,111 | 20 |
| jsonb / 25Rows / 16Elements / TypedFloatControl | 0.561 (0.529–0.765) → 0.536 (0.48–0.555) | 1.497 (1.416–1.548) → 1.516 (1.438–1.554) | 61 → 61 | 9,243 → 9,243 | 20 |
| jsonb / 25Rows / 16Elements / AnyNumbers | 1.28 (1.17–1.633) → 1.212 (1.166–1.618) | 2.34 (2.158–2.691) → 2.242 (2.03–2.334) | 151 → 149 | 33,094 → 32,533 | 20 |
| jsonb / 25Rows / 16Elements / AnyStrings | 0.579 (0.515–0.75) → 0.558 (0.525–0.72) | 1.118 (1.027–1.214) → 1.007 (0.944–1.205) | 59 → 57 | 8,982 → 8,421 | 20 |
| jsonb / 25Rows / 16Elements / AnyInvalidNested | 0.505 (0.483–0.545) → 0.557 (0.478–0.706) | 1.698 (1.591–1.86) → 1.693 (1.553–1.88) | 53 → 51 | 8,224 → 7,663 | 22 |
| jsonb / 25Rows / 16Elements / AnyInvalidMixed | 0.485 (0.463–0.496) → 0.551 (0.487–0.576) | 0.772 (0.746–0.793) → 0.718 (0.664–0.77) | 53 → 51 | 8,224 → 7,663 | 22 |
| jsonb / 25Rows / 16Elements / RepeatedSort | 1.62 (1.518–1.71) → 1.57 (1.446–1.73) | 3.679 (3.635–3.949) → 3.442 (3.263–3.884) | 182 → 178 | 39,070 → 38,167 | 20 |
| jsonb / 100Rows / 16Elements / TypedIntControl | 0.316 (0.304–0.404) → 0.358 (0.308–0.46) | 3.421 (3.339–3.556) → 3.607 (3.526–3.82) | 35 → 35 | 3,111 → 3,111 | 80 |
| jsonb / 100Rows / 16Elements / TypedFloatControl | 0.552 (0.526–0.651) → 0.598 (0.49–0.754) | 5.751 (5.582–6.067) → 5.944 (5.705–6.332) | 61 → 61 | 9,243 → 9,243 | 80 |
| jsonb / 100Rows / 16Elements / AnyNumbers | 1.219 (1.192–1.286) → 1.385 (1.181–1.545) | 8.601 (8.241–9.148) → 8.008 (7.813–8.598) | 151 → 149 | 33,094 → 32,533 | 80 |
| jsonb / 100Rows / 16Elements / AnyStrings | 0.579 (0.522–0.679) → 0.655 (0.537–0.79) | 4.116 (3.912–4.393) → 3.865 (3.707–4.061) | 59 → 57 | 8,982 → 8,421 | 80 |
| jsonb / 100Rows / 16Elements / AnyInvalidNested | 0.546 (0.535–0.589) → 0.554 (0.449–0.714) | 6.553 (6.502–6.82) → 6.4 (6.059–6.756) | 53 → 51 | 8,224 → 7,663 | 86 |
| jsonb / 100Rows / 16Elements / AnyInvalidMixed | 0.533 (0.49–0.674) → 0.489 (0.46–0.719) | 2.798 (2.696–2.938) → 2.662 (2.542–2.77) | 53 → 51 | 8,224 → 7,663 | 86 |
| jsonb / 100Rows / 16Elements / RepeatedSort | 1.519 (1.472–1.678) → 1.522 (1.421–2.051) | 14.225 (13.55–14.92) → 12.825 (12.56–13.33) | 182 → 178 | 39,070 → 38,167 | 80 |
| jsonb / 25Rows / 64Elements / TypedIntControl | 0.317 (0.28–0.331) → 0.337 (0.269–0.396) | 3.734 (3.652–3.883) → 3.779 (3.708–3.988) | 35 → 35 | 3,111 → 3,111 | 20 |
| jsonb / 25Rows / 64Elements / TypedFloatControl | 0.538 (0.503–0.65) → 0.585 (0.546–0.739) | 5.794 (5.568–5.927) → 5.855 (5.634–6.001) | 61 → 61 | 9,243 → 9,243 | 20 |
| jsonb / 25Rows / 64Elements / AnyNumbers | 1.217 (1.087–1.604) → 1.224 (1.104–1.456) | 8.568 (8.425–9.012) → 7.966 (7.82–8.561) | 151 → 149 | 33,094 → 32,533 | 20 |
| jsonb / 25Rows / 64Elements / AnyStrings | 0.564 (0.502–0.658) → 0.546 (0.502–0.607) | 3.216 (3.123–3.369) → 3.157 (3.069–3.315) | 59 → 57 | 8,982 → 8,421 | 20 |
| jsonb / 25Rows / 64Elements / AnyInvalidNested | 0.536 (0.48–0.656) → 0.498 (0.485–0.614) | 8.329 (7.914–8.408) → 8.108 (7.88–8.449) | 53 → 51 | 8,224 → 7,663 | 22 |
| jsonb / 25Rows / 64Elements / AnyInvalidMixed | 0.515 (0.475–0.578) → 0.524 (0.475–0.703) | 3.128 (3.073–3.364) → 2.986 (2.895–3.215) | 53 → 51 | 8,224 → 7,663 | 22 |
| jsonb / 25Rows / 64Elements / RepeatedSort | 1.528 (1.447–1.701) → 1.683 (1.496–1.952) | 14.005 (13.5–14.23) → 12.455 (12.21–13.02) | 182 → 178 | 39,070 → 38,167 | 20 |
| jsonb / 100Rows / 64Elements / TypedIntControl | 0.306 (0.292–0.325) → 0.319 (0.304–0.398) | 15.255 (15.06–15.78) → 15.805 (15.37–16.22) | 35 → 35 | 3,111 → 3,111 | 80 |
| jsonb / 100Rows / 64Elements / TypedFloatControl | 0.558 (0.533–0.684) → 0.605 (0.537–0.652) | 23.31 (22.32–24.4) → 23.805 (23.51–24.39) | 61 → 61 | 9,243 → 9,243 | 80 |
| jsonb / 100Rows / 64Elements / AnyNumbers | 1.178 (1.134–1.214) → 1.381 (1.215–1.643) | 33.81 (33.07–35.07) → 31.955 (31.13–32.67) | 151 → 149 | 33,094 → 32,533 | 80 |
| jsonb / 100Rows / 64Elements / AnyStrings | 0.571 (0.515–0.599) → 0.595 (0.536–0.669) | 13.005 (12.79–13.33) → 12.58 (12.18–13.06) | 59 → 57 | 8,982 → 8,421 | 80 |
| jsonb / 100Rows / 64Elements / AnyInvalidNested | 0.542 (0.488–0.631) → 0.555 (0.48–0.682) | 32.125 (30.38–33.21) → 32.08 (31.75–32.67) | 53 → 51 | 8,224 → 7,663 | 86 |
| jsonb / 100Rows / 64Elements / AnyInvalidMixed | 0.52 (0.498–0.583) → 0.565 (0.495–0.589) | 12.405 (12–12.63) → 12.04 (11.6–12.24) | 53 → 51 | 8,224 → 7,663 | 86 |
| jsonb / 100Rows / 64Elements / RepeatedSort | 1.607 (1.439–1.742) → 1.512 (1.461–2.119) | 55.85 (55.02–57.45) → 49.97 (49.22–50.51) | 182 → 178 | 39,070 → 38,167 | 80 |

At 100 rows and 64 elements, the measured JSON `AnyNumbers`, `AnyStrings`, and
`RepeatedSort` execution medians changed from 22.62 to 19.47 ms, 6.225 to
5.466 ms, and 43.66 to 38.065 ms. The corresponding JSONB metadata-backed
medians were 33.81 to 31.955 ms for `AnyNumbers` and 55.85 to 49.97 ms for
`RepeatedSort`; the JSONB `AnyInvalidNested` workload was effectively unchanged
at 32.125 versus 32.08 ms. These are workload-specific observations, not a
general execution guarantee. Build timing was not uniformly lower, some
candidate workloads allocated more bytes despite fewer allocations, and
control timings drifted despite identical SQL. The benchmark validates predicate
results and plan cardinality; it does not claim to compare every sorted output
value. Focused PostgreSQL/local parity tests cover selected sort values,
subtypes, stable ties, JSON whitespace and escapes, underflow-to-zero, repeated
sorts, and invalid shapes.

## Correctness and tradeoffs

The PostgreSQL/local dynamic-sort parity matrix passed 2,312 comparisons. The
project check and integration-tagged SQL vet passed. The correctness tests
include JSONB metadata restoration and confirm that ordinary JSONB Float schema
information cannot recover a negative-zero sign that storage already erased.
The combined candidate reduces SQL bytes and plan-node counts for measured
dynamic workloads while retaining normalization where JSONB representation
recovery needs it. It does not alter HFF collection semantics or the schema-
resolved homogeneous path. No retained-memory, peak-memory, production, or
cross-version measurement was made.

## Decision and limits

Retain the bounded lowering for the measured dynamic JSON sort shapes. The
measured improvements vary by JSON format and workload; the JSONB invalid-nested
case and typed controls do not show a consistent execution benefit. Do not
extrapolate these local measurements to other schemas, PostgreSQL versions,
list sizes, data distributions, or service latency. Further optimization of
dynamic sort construction requires its own paired correctness and measurement
evidence. See the [PostgreSQL optimization catalog](../backends/postgresql/optimizations.md#sort-recoverable-dynamic-json-lists)
for the translation scope.
