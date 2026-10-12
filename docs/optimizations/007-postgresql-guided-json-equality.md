# 007. Specialize recoverable PostgreSQL JSON equality

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

Equality between JSON-backed values can require a recursive runtime walk to
recover each node's type and structure. The candidate uses the declared root
schema when it makes a comparison path definite: lists compare by ordinal,
objects by key set, and nested values use the existing typed recursion. This
also applies when children are `Any`. An `Any` value compared with one known
scalar family uses a category check and that scalar's exact comparison
instead of a generic tree walk.

Both operands still pass through the existing representation-recovery path.
JSONB metadata requirements, nested-null equality, absent-versus-null object
keys, and top-level null/no-value behavior are unchanged. Comparisons with two
unconstrained roots, an unconstrained root against a container, or overlapping
structural unions retain the generic recursive JSON comparison.

## Applicability

| Comparison shape | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Two definitely same-category roots (list/list or object/object) | Applied | The typed length/key-set recursion can compare children, including `Any`, without generic tree normalization. |
| `Any` compared with one known scalar category | Applied | Runtime category and exact scalar comparison are selected from the known side. |
| Two `Any` roots or `Any` root against a container | Not applied | The generic recursive tree comparison remains necessary. |
| Overlapping list/map/struct union roots | Not applied | The specialization excludes ambiguous overlapping structural members. |
| Typed-list and unconstrained-root-versus-list-literal controls | Not applicable | These controls measure unchanged paths. |

## Reproduction

The baseline is [258cacd](https://github.com/ornew/hff/commit/258cacd7511227522185d36ea770d85a1a38fe70). The candidate source snapshot SHA-256 values were:

| File | SHA-256 |
|:--|:--|
| `backends/sql/json_equality.go` | `5a0813d97ac2a7394165807d07e5a8aa68ff91abd8a683c627bfb06093e7297e` |
| `backends/sql/json_equal_scalar.go` | `2f4b2180d775d28651f2933b1ebca659b91ccae1360988323ded258a80f7fc16` |
| `backends/sql/comparison_bench_test.go` | `1ca775419f0bf06e5439d1a6e41b24d924dae3510deed5ee3a762416cb949157` |
| `backends/sql/comparison_execution_bench_test.go` | `a410ac6de9b5faa194ea8cec5d219bc051d428b3c9d0fbef645f38127110cfe4` |
| `backends/sql/comparison_test.go` | `52403c26de67f3e52541384b6e150f9c8aa75e0b29db6e849f09a5f0008e62be` |
| `backends/sql/comparison_integration_test.go` | `a447106d6710479e9df594527d7425dfc453e3f6afb13e842904ae95c9d0315d` |

The baseline revision was extended with the same comparison harness, fixtures,
and `testing.TB` helper-signature changes used for the candidate. The
`AnyRootControl` schema is unconstrained `Any` compared with a list literal.
Build measurements covered `TypedListControl`, `AnyRootControl`,
`AnyList`, `AnyMap`, and `AnyStruct` for native JSON and metadata-backed JSONB.
Database measurements used the same ten format/shape combinations at 50 and
250 rows. Each execution sample checked SQL result IDs against local HFF
matches and the fixture's expected IDs before measuring PostgreSQL planning
and execution. Query values included `[1, 2, null]` and
`{"price": 1, "empty": null}`. Fixtures also covered mixed integer/float
values, null roots, short and long lists, and missing versus extra object keys.

The host was Darwin 25.2.0, arm64, Apple M3 Max with 64 GiB RAM. Go was 1.27.1
with default `GOMAXPROCS=16`. PostgreSQL was 18.6 in the same Debian aarch64
container image used by the surrounding metadata measurements
(`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`).
The database directory used tmpfs; there were no explicit CPU, memory, or
cpuset limits. JIT was off, parallel workers were disabled, plans were forced
custom, and statement timeout was 60 seconds. Two serialized baseline/candidate
pairs were run; tables report the final three-sample pair. The query-result
check precedes each `EXPLAIN` sample.

Build command:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLComparisonBuild$/(json|jsonb)/(TypedListControl|AnyRootControl|AnyList|AnyMap|AnyStruct)$' -benchmem \
  -benchtime=200ms -count=3
```

Database execution command (use separate plan directories for baseline and
candidate):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-comparison-plans \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLComparisonExecution$/(json|jsonb)/(50Rows|250Rows)/(TypedListControl|AnyRootControl|AnyList|AnyMap|AnyStruct)$' \
  -benchtime=1x -count=3
```

Raw logs, SQL, plans, and fixture results are outside Git in
`/private/tmp/hff-comparison-plan-evidence/` on the measurement host.

## Results

Build times are median ns/op with the three-sample min–max. `B/op` is cumulative
allocation, not peak or retained memory.

| Format / shape | Baseline ns/op (range) | Candidate ns/op (range) | B/op baseline → candidate | allocs/op baseline → candidate | SQL bytes / parameters baseline → candidate |
|:--|--:|--:|--:|--:|--:|
| JSON / TypedListControl | 20,830 (20,821–23,565) | 21,188 (20,745–23,323) | 37,745 → 37,747 | 205 → 205 | 1,608 / 2 → 1,608 / 2 |
| JSON / AnyRootControl | 165,146 (162,879–172,445) | 163,561 (163,456–163,661) | 1,421,100 → 1,421,098 | 369 → 369 | 112,222 / 2 → 112,222 / 2 |
| JSON / AnyList | 92,545 (92,219–94,098) | 54,523 (54,494–54,609) | 695,728 → 357,091 | 276 → 227 | 57,320 / 2 → 26,292 / 2 |
| JSON / AnyMap | 97,528 (95,521–110,458) | 61,565 (61,472–61,736) | 715,393 → 414,150 | 306 → 264 | 57,638 / 5 → 26,737 / 5 |
| JSON / AnyStruct | 100,899 (99,504–108,264) | 62,675 (62,495–62,840) | 751,512 → 415,376 | 362 → 298 | 57,924 / 7 → 26,737 / 5 |
| JSONB metadata / TypedListControl | 22,627 (22,284–23,110) | 22,025 (21,985–23,029) | 44,713 → 44,714 | 215 → 215 | 3,302 / 2 → 3,302 / 2 |
| JSONB metadata / AnyRootControl | 167,980 (167,518–173,460) | 171,713 (166,362–186,985) | 1,424,786 → 1,424,797 | 379 → 379 | 113,924 / 2 → 113,924 / 2 |
| JSONB metadata / AnyList | 96,014 (95,090–96,183) | 56,494 (56,162–56,654) | 715,878 → 363,583 | 286 → 237 | 59,062 / 2 → 28,004 / 2 |
| JSONB metadata / AnyMap | 96,851 (96,529–97,089) | 63,257 (63,038–63,685) | 719,093 → 420,774 | 316 → 274 | 59,380 / 5 → 28,479 / 5 |
| JSONB metadata / AnyStruct | 107,181 (102,166–112,326) | 63,731 (63,573–64,103) | 755,246 → 422,020 | 372 → 308 | 59,666 / 7 → 28,479 / 5 |

Database timing tables report median milliseconds and the three-sample
min–max. Plan nodes and SQL bytes report baseline to candidate; every case had
the same match count in both revisions.

| Format / rows / shape | Planning ms baseline (range) | Planning ms candidate (range) | Execution ms baseline (range) | Execution ms candidate (range) | Nodes baseline → candidate | SQL bytes baseline → candidate | matched rows |
|:--|--:|--:|--:|--:|--:|--:|--:|
| JSON / 50 / TypedListControl | 0.110 (0.105–0.131) | 0.119 (0.107–0.123) | 0.203 (0.193–0.246) | 0.215 (0.210–0.218) | 12 → 12 | 1,608 → 1,608 | 35 |
| JSON / 50 / AnyRootControl | 3.619 (3.532–3.921) | 3.673 (3.601–3.741) | 3.075 (3.025–3.113) | 3.113 (3.112–3.180) | 445 → 445 | 112,222 → 112,222 | 33 |
| JSON / 50 / AnyList | 1.922 (1.906–2.042) | 0.929 (0.927–1.080) | 2.952 (2.947–3.059) | 0.906 (0.882–0.919) | 236 → 107 | 57,320 → 26,292 | 33 |
| JSON / 50 / AnyMap | 2.230 (2.122–2.466) | 0.987 (0.924–0.993) | 2.494 (2.492–2.553) | 0.768 (0.752–0.775) | 236 → 110 | 57,638 → 26,737 | 33 |
| JSON / 50 / AnyStruct | 1.889 (1.870–2.127) | 1.006 (0.962–1.093) | 2.540 (2.523–2.709) | 0.767 (0.747–0.789) | 236 → 110 | 57,924 → 26,737 | 33 |
| JSON / 250 / TypedListControl | 0.122 (0.110–0.129) | 0.109 (0.107–0.117) | 0.819 (0.800–0.853) | 0.844 (0.765–0.849) | 12 → 12 | 1,608 → 1,608 | 179 |
| JSON / 250 / AnyRootControl | 3.541 (3.467–3.585) | 3.566 (3.501–3.583) | 11.950 (11.910–12.030) | 12.710 (12.150–12.730) | 445 → 445 | 112,222 → 112,222 | 169 |
| JSON / 250 / AnyList | 2.103 (2.058–2.635) | 0.973 (0.903–0.976) | 13.270 (12.770–13.630) | 3.805 (3.719–3.906) | 236 → 107 | 57,320 → 26,292 | 169 |
| JSON / 250 / AnyMap | 2.502 (2.401–2.955) | 1.038 (1.002–1.041) | 12.000 (11.750–12.240) | 3.182 (3.126–3.198) | 236 → 110 | 57,638 → 26,737 | 169 |
| JSON / 250 / AnyStruct | 2.081 (2.009–2.442) | 1.010 (0.971–1.086) | 12.080 (11.850–12.660) | 3.589 (3.390–3.623) | 236 → 110 | 57,924 → 26,737 | 169 |
| JSONB metadata / 50 / TypedListControl | 0.327 (0.324–0.592) | 0.262 (0.240–0.305) | 0.636 (0.549–0.754) | 0.647 (0.602–0.659) | 28 → 28 | 3,302 → 3,302 | 35 |
| JSONB metadata / 50 / AnyRootControl | 4.423 (4.041–4.887) | 4.116 (3.997–4.467) | 3.380 (3.151–3.904) | 3.325 (3.259–3.376) | 461 → 461 | 113,924 → 113,924 | 33 |
| JSONB metadata / 50 / AnyList | 2.490 (2.192–2.862) | 1.076 (1.072–1.111) | 2.974 (2.910–3.095) | 1.304 (1.302–1.328) | 252 → 123 | 59,062 → 28,004 | 33 |
| JSONB metadata / 50 / AnyMap | 2.311 (2.285–2.382) | 1.216 (1.095–1.279) | 2.812 (2.781–2.949) | 1.173 (1.086–1.183) | 252 → 126 | 59,380 → 28,479 | 33 |
| JSONB metadata / 50 / AnyStruct | 2.249 (2.189–2.368) | 1.145 (1.108–1.216) | 2.880 (2.822–2.938) | 1.154 (1.130–1.230) | 252 → 126 | 59,666 → 28,479 | 33 |
| JSONB metadata / 250 / TypedListControl | 0.263 (0.254–0.295) | 0.298 (0.282–0.317) | 2.538 (2.509–2.688) | 2.539 (2.457–2.658) | 28 → 28 | 3,302 → 3,302 | 179 |
| JSONB metadata / 250 / AnyRootControl | 3.926 (3.888–3.949) | 3.935 (3.926–4.000) | 14.400 (14.230–14.450) | 14.150 (14.020–14.410) | 461 → 461 | 113,924 → 113,924 | 169 |
| JSONB metadata / 250 / AnyList | 2.260 (2.155–2.359) | 1.138 (1.081–1.160) | 14.050 (13.650–14.130) | 5.677 (5.653–5.690) | 252 → 123 | 59,062 → 28,004 | 169 |
| JSONB metadata / 250 / AnyMap | 2.183 (2.133–2.342) | 1.146 (1.085–1.306) | 12.930 (12.690–13.070) | 4.931 (4.865–5.110) | 252 → 126 | 59,380 → 28,479 | 169 |
| JSONB metadata / 250 / AnyStruct | 2.226 (2.145–2.260) | 1.081 (1.057–1.108) | 13.110 (13.060–13.340) | 5.119 (4.976–5.148) | 252 → 126 | 59,666 → 28,479 | 169 |

The typed-list and unconstrained-root-versus-list-literal controls retain
byte-identical SQL and plan-node counts; their timings vary between samples.
For the definite container workloads, generated SQL and plan size are smaller
in this fixture.
These measurements do not establish a general database latency improvement or
an index-use guarantee. Build `B/op` is cumulative allocation; live or peak
memory was not measured.

## Correctness and tradeoffs

The tests cover null roots, empty containers, nested nulls, missing versus
present-null keys, list length/order, object key sets, numeric subtypes, and
metadata-required JSONB representations. Both sides still use
`comparisonDocumentAgainst`, so specialization does not bypass numeric
representability checks. The root type controls whether a typed recursive plan
is used; a definite container pair can recurse into `Any` children. Generic
runtime-tree equality remains for two unconstrained roots, root-`Any` versus a
container, and ambiguous structural unions. No retained-memory measurement was
made.

Candidate validation:

- `TMPDIR=/private/tmp HFF_WASM_NODE=<node-path> GOTOOLCHAIN=go1.27.1 sh hack/check.sh` passed, including formatting, race tests, vet, generation checks, examples, WASM, and site checks.
- `TMPDIR=/private/tmp GOTOOLCHAIN=go1.27.1 go vet -tags integration ./backends/sql` passed.
- `TMPDIR=/private/tmp GOTOOLCHAIN=go1.27.1 HFF_POSTGRES_CONTAINER=<container-name> go test -tags integration ./backends/sql -count=1` passed the full PostgreSQL integration suite (352.541s).

## Decision and limits

Retain the measured specialization for definite same-category containers and
`Any`-to-known-scalar comparisons. The controls show unchanged SQL shape for
typed lists and an unconstrained root compared with a list literal. The result
applies to these schemas, fixtures, and PostgreSQL profile; it does not imply
lower latency for every query or remove JSONB metadata requirements.
