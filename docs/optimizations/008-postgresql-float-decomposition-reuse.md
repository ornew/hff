# 008. Reuse exact Float decomposition within SQL scopes

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

Exact PostgreSQL Float arithmetic lowers each HFF operand to its original
Float SQL expression and an exact decomposition used by the arithmetic
helpers. Repeated references to the same operand rebuilt those SQL fragments
within a projection scope. The candidate caches the original Float expression
and its exact decomposition separately in the existing `sharedPaths` scope.
The final two-pass lowering preserves the original first-use order. It does not
merge equal values from distinct SQL expressions, move work across scopes, or
change per-operation presence handling. Caches reset at existing projection
scope boundaries.

## Applicability

| Workload | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| A repeated Float operand in one projection scope | Applied | The final query reuses the original expression and exact decomposition. |
| A single-use Float operand | Partial | It follows the same cache path and adds Build allocations; generated SQL remains byte-identical. |
| Distinct SQL operand expressions or distinct projection scopes | Not applied | Reuse requires the same SQL expression in the same scope; scope boundaries remain isolated. |
| Database evaluation of a repeated expression | Partial | The repeated-query SQL and plan shrink in this fixture; SQL does not promise PostgreSQL's physical evaluation order. |

## Reproduction

The baseline is [cdeff058](https://github.com/ornew/hff/commit/cdeff0580b14c06e8eaa05c2771d22db73ab42dc). The candidate `backends/sql/float_arithmetic.go` source snapshot has SHA-256 `6ec771e0289345f64dc1d67728e6184fd48718921ce554e1040c70c5392ffe78`. The same arithmetic benchmark harness was added to the baseline and candidate:

- [Build benchmark](https://github.com/ornew/hff/blob/main/backends/sql/arithmetic_bench_test.go), SHA-256 `afc75ea7d799da1e72ad9700065e845fe3fd46ada460a1abe9b67b2936fc68c2`
- [Execution benchmark](https://github.com/ornew/hff/blob/main/backends/sql/arithmetic_execution_bench_test.go), SHA-256 `4282521126b944e54155490c99638533b17677788fe163c7c73ea51fe1baaab8`

The eight workloads were `CompareControl`, `Add`, `Subtract`, `Multiply`,
`Divide`, `Repeated`, `Guarded`, and `Constant`:

| Workload | HFF query |
|:--|:--|
| CompareControl | `left>right` |
| Add | `left+right>0` |
| Subtract | `left-right>0` |
| Multiply | `left*right>0` |
| Divide | `left/right>0` |
| Repeated | `left+right>0 left-right<100 left*right!=0` |
| Guarded | `left!=null right!=null left+right>0` |
| Constant | `left+0.1>0` |

PostgreSQL execution covered all eight at 100 and 1,000 rows. The fixture
included signed zero, subnormals, maximum finite Float, and null operands. The
`Guarded` query checks null predicates; no-value guard behavior was covered by
separate correctness tests. Each database sample checked returned IDs against
local HFF before collecting PostgreSQL planning and execution. Only the
`Repeated` SQL changed; the other 14 scripts were byte-identical.

The host was Darwin 25.2.0, arm64, Apple M3 Max with 64 GiB RAM. Go was 1.27.1
with default `GOMAXPROCS=16`. PostgreSQL was 18.6 in a Debian aarch64 container
with image digest `sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`.
There were no explicit CPU, memory, or cpuset limits. JIT was off, parallel
workers were disabled, custom plans were forced, and statement timeout was
60 seconds. The database directory used tmpfs. No retained-heap measurement
was made.

Build command (three 200 ms samples per sub-benchmark):

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLArithmeticBuild$/(CompareControl|Add|Subtract|Multiply|Divide|Repeated|Guarded|Constant)$' \
  -benchmem -benchtime=200ms -count=3
```

Execution command (use separate plan directories for baseline and candidate):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-arithmetic-plans \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLArithmeticExecution$/(100Rows|1000Rows)/(CompareControl|Add|Subtract|Multiply|Divide|Repeated|Guarded|Constant)$' \
  -benchtime=1x -count=3
```

Raw logs, SQL, plans, and IDs remain outside Git in
`/private/tmp/hff-arithmetic-evidence/` on the measurement host. The final
baseline and candidate logs report the final paired source snapshots; earlier
exploratory candidate runs are excluded.

## Results

Build time is median ns/op with the three-sample min–max. Allocation is
cumulative `B/op`, not retained or peak memory. The final candidate adds about
255 bytes and eight allocations to single-use arithmetic workloads; allocation
counts fall for the repeated workload as generated SQL is shared.

| Workload | Baseline ns/op (range) | Candidate ns/op (range) | B/op baseline → candidate | allocs/op baseline → candidate | SQL bytes baseline → candidate |
|:--|--:|--:|--:|--:|--:|
| CompareControl | 20,392 (20,027–21,354) | 19,046 (19,005–19,370) | 23,496 → 23,496 | 162 → 162 | 268 → 268 |
| Add | 27,134 (27,037–27,824) | 26,946 (26,914–27,033) | 60,066 → 60,321 | 248 → 256 | 6,023 → 6,023 |
| Subtract | 27,477 (26,992–27,571) | 27,321 (26,768–27,968) | 60,066 → 60,322 | 248 → 256 | 6,027 → 6,027 |
| Multiply | 28,390 (27,407–28,714) | 26,589 (26,507–28,564) | 59,520 → 59,776 | 245 → 253 | 5,983 → 5,983 |
| Divide | 33,199 (33,165–33,396) | 31,990 (31,974–32,608) | 92,216 → 92,468 | 296 → 304 | 11,093 → 11,093 |
| Repeated | 46,980 (46,780–48,126) | 37,243 (37,202–37,857) | 137,264 → 96,227 | 523 → 439 | 17,968 → 11,618 |
| Guarded | 30,347 (30,302–30,484) | 29,883 (29,789–29,960) | 62,760 → 63,015 | 317 → 325 | 6,356 → 6,356 |
| Constant | 26,612 (26,113–27,876) | 26,360 (25,730–26,760) | 59,417 → 59,675 | 213 → 221 | 5,961 → 5,961 |

Database results below are medians of three one-execution samples. Planning
and execution timings for one-use cases vary only slightly in this selected
fixture; the large difference is confined to `Repeated`.

| Rows / workload | Planning ms baseline → candidate | Execution ms baseline → candidate | Plan nodes baseline → candidate | SQL bytes baseline → candidate | Matched rows |
|:--|--:|--:|--:|--:|--:|
| 100 / CompareControl | 0.035 (0.034–0.050) → 0.044 (0.032–0.045) | 0.041 (0.040–0.042) → 0.043 (0.041–0.048) | 6 → 6 | 268 → 268 | 21 |
| 100 / Add | 0.444 (0.434–0.473) → 0.497 (0.463–0.546) | 0.859 (0.842–0.906) → 0.917 (0.855–0.919) | 32 → 32 | 6,023 → 6,023 | 74 |
| 100 / Subtract | 0.469 (0.463–0.498) → 0.471 (0.430–0.473) | 0.874 (0.833–0.885) → 0.875 (0.833–0.896) | 32 → 32 | 6,027 → 6,027 | 21 |
| 100 / Multiply | 0.467 (0.460–0.476) → 0.511 (0.462–0.614) | 0.930 (0.927–0.947) → 0.929 (0.831–1.128) | 32 → 32 | 5,983 → 5,983 | 52 |
| 100 / Divide | 0.662 (0.645–0.709) → 0.652 (0.618–0.655) | 1.733 (1.686–1.746) → 1.696 (1.656–1.712) | 50 → 50 | 11,093 → 11,093 | 52 |
| 100 / Repeated | 1.280 (1.240–1.491) → 0.830 (0.742–0.863) | 2.803 (2.572–2.940) → 1.341 (1.244–1.477) | 90 → 54 | 17,968 → 11,618 | 52 |
| 100 / Guarded | 0.436 (0.434–0.654) → 0.427 (0.425–0.513) | 0.974 (0.960–1.014) → 0.903 (0.879–0.904) | 38 → 38 | 6,356 → 6,356 | 74 |
| 100 / Constant | 0.417 (0.393–0.464) → 0.392 (0.387–0.432) | 0.860 (0.824–0.909) → 0.798 (0.755–0.830) | 30 → 30 | 5,961 → 5,961 | 86 |
| 1,000 / CompareControl | 0.042 (0.032–0.044) → 0.041 (0.039–0.044) | 0.316 (0.312–0.317) → 0.309 (0.309–0.326) | 6 → 6 | 268 → 268 | 224 |
| 1,000 / Add | 0.497 (0.459–0.513) → 0.463 (0.461–0.466) | 8.410 (7.980–8.587) → 7.984 (7.875–8.169) | 32 → 32 | 6,023 → 6,023 | 743 |
| 1,000 / Subtract | 0.491 (0.486–0.494) → 0.466 (0.440–0.540) | 8.340 (8.224–8.398) → 7.888 (7.853–7.892) | 32 → 32 | 6,027 → 6,027 | 224 |
| 1,000 / Multiply | 0.501 (0.460–0.540) → 0.442 (0.437–0.480) | 8.509 (8.489–8.695) → 8.334 (8.245–8.366) | 32 → 32 | 5,983 → 5,983 | 521 |
| 1,000 / Divide | 0.699 (0.680–0.767) → 0.633 (0.593–0.650) | 14.950 (14.660–15.060) → 14.660 (14.490–14.700) | 50 → 50 | 11,093 → 11,093 | 521 |
| 1,000 / Repeated | 1.323 (1.191–1.502) → 0.818 (0.789–0.820) | 26.490 (26.490–26.900) → 12.300 (12.210–12.530) | 90 → 54 | 17,968 → 11,618 | 519 |
| 1,000 / Guarded | 0.471 (0.449–0.480) → 0.474 (0.451–0.491) | 8.761 (8.540–8.865) → 8.590 (8.387–8.918) | 38 → 38 | 6,356 → 6,356 | 743 |
| 1,000 / Constant | 0.473 (0.434–0.477) → 0.444 (0.396–0.469) | 7.878 (7.760–7.991) → 7.613 (7.603–7.705) | 30 → 30 | 5,961 → 5,961 | 863 |

## Correctness and tradeoffs

Focused PostgreSQL parity tests cover repeated same-operand arithmetic,
guards, captured and shadowed callback variables, and folds. The cache changes
SQL only when a shared operand is reused; it does not alter operand order,
numeric decomposition, exact arithmetic, or per-call presence behavior. The
single-use cases show the Build cost of maintaining the cache. PostgreSQL does
not guarantee physical expression evaluation order, and no retained-heap or
production-service measurements were made.

Candidate validation:

- `TMPDIR=/private/tmp HFF_WASM_NODE=<node-path> GOTOOLCHAIN=go1.27.1 sh hack/check.sh` passed, including formatting, race tests, vet, generation checks, examples, WASM, and site checks.
- `TMPDIR=/private/tmp GOTOOLCHAIN=go1.27.1 go vet -tags integration ./backends/sql` passed.
- `TMPDIR=/private/tmp GOTOOLCHAIN=go1.27.1 HFF_POSTGRES_CONTAINER=<container-name> go test -tags integration ./backends/sql -count=1` passed the full PostgreSQL integration suite (357.439s).

## Decision and limits

Retain scoped reuse of the original Float expression and exact decomposition
for repeated operands. The measured Build and PostgreSQL changes apply to the
listed workloads and environment; they do not imply a general speedup for
single-use arithmetic or other query shapes.
