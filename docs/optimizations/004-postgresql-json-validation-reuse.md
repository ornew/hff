# 004. Reuse PostgreSQL JSON parsing and validation results

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

`fromjson()` validates a complete JSON document before exposing its HFF value.
The baseline repeated recursive numeric-validation SQL for repeated uses of
the same projected receiver. The candidate caches the fully validated JSON
result by exact receiver SQL text within the current projection scope. Every
call keeps its own presence/no-value expression; callback and shadowed-alias
scopes remain isolated. This is expression reuse, not value common-subexpression
elimination across different SQL expressions.

The validation contract is unchanged. PostgreSQL checks one complete document
for JSON syntax, decoded Unicode, and duplicate keys. A recursive walk validates
every numeric node against HFF int64 and finite-float rules. An invalid nested
number makes the whole parse ordinary null even if later access would not read
that field. Boolean, string, and null roots skip the recursive numeric walk; a
number root still validates its single number. Array/object recursion does not
expand scalar leaf children. The validated result is projected once and reused
within its scope.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Same projected `fromjson()` receiver repeated in one projection scope | Applied | Reuses the fully validated JSON result while keeping per-call presence. |
| One use of a receiver | Partial | No reuse occurs, but scalar leaves are not expanded during recursive validation. |
| Different receiver expressions with equal text content | Not applied | The cache keys SQL expressions, not runtime string values. |
| Separate callback or shadowed-alias scope | Not applied | Projection scopes have isolated cache entries. |
| Boolean, string, or null root | Partial | Syntax, Unicode, and duplicate-key checks remain; the recursive numeric walk is skipped. |
| Number, array, or object root | Partial | Numeric validation remains for the number or all nested numeric values; scalar leaves are not expanded. |

## Reproduction

The baseline is [f2421d84](https://github.com/ornew/hff/commit/f2421d84ed36f1ffa8242b8b944cb500ba7a6b2e). The candidate was measured as a source snapshot; its SHA-256 inputs are listed below. Earlier candidate-run timings differed, so this record reports only the final baseline/candidate outputs.

| Candidate source file | SHA-256 |
|:--|:--|
| `backends/sql/json.go` | `cb69f7ff7719aecca7e276d80d7b2c223bdb7b41022753f948f3cd3928f74c22` |
| `backends/sql/json_parse.go` | `af171c0fb12df43f8a8a517abfd6bd11583054a1d50484fff0a5796937c52fb1` |
| `backends/sql/json_decode.go` | `b50a6bd1ca5c045224e117b91c2c5e5836af3df7e1675de9b3d10e71fbfa481b` |
| `backends/sql/json_parse_bench_test.go` | `f32314953483a238d26f82040814477b7411aef8a95de3b9682141d0b9ce7845` |
| `backends/sql/json_parse_execution_bench_test.go` | `c25bba5a9c6f88661f6f9c23b0eed9fd7c790dfdb53256b955caf31a37d8e3a4` |

The host was Darwin 25.2.0, `darwin/arm64`, Apple M3 Max with 64 GiB RAM. Go
was 1.27.1 and benchmark output used the default `GOMAXPROCS=16`. PostgreSQL was
18.6 in a Debian aarch64 container using image
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`. The
container had no explicit CPU, memory, or cpuset limits; its data directory used
tmpfs, so these results do not measure persistent-disk I/O. JIT was disabled,
`max_parallel_workers_per_gather` was zero, custom plans were forced, and the
statement timeout was 60 seconds.

The same benchmark harness and fixtures were applied to baseline and candidate
source snapshots. The harness used nine query shapes and 100- and 1,000-row
temporary tables: scalar number/string/bool roots, a single object field,
repeated object access, an array predicate, constant valid and invalid-nested
documents, and repeated constants. Exact query shapes are defined by
`BenchmarkPostgreSQLJSONParsingBuild` in
[`json_parse_bench_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_parse_bench_test.go) and
`BenchmarkPostgreSQLJSONParsingExecution` in
[`json_parse_execution_bench_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_parse_execution_bench_test.go).
Fixtures included nulls, malformed JSON, duplicate keys, and out-of-range
nested numbers. Before each `EXPLAIN`, exact SQL result IDs were compared with
local HFF matches. Server planning and execution came from
`EXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS)`; fixture setup,
Docker/psql startup, and validation-query time are not reported as database
execution time.

Build measurements:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' -bench '^BenchmarkPostgreSQLJSONParsingBuild$' -benchtime=200ms -count=3
```

PostgreSQL measurements (use a unique plan directory for each revision):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-parsing-plans GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' -bench '^BenchmarkPostgreSQLJSONParsingExecution$' -benchtime=1x -count=3
```

Raw baseline/candidate logs, plans, SQL, and summary are outside Git in
`/private/tmp/hff-parsing-evidence/` on the measurement host.

## Results

Values are medians of three samples. Time ranges show minimum–maximum. Build
`B/op` is cumulative Go allocation, not peak or retained memory.

| Workload | Baseline ns/op (min–max) | Candidate ns/op (min–max) | B/op | allocs/op | SQL bytes |
|:--|--:|--:|--:|--:|--:|
| ScalarNumber | 21201 ns (21080–23796) | 31930 ns (27149–37502) | 54,882 → 54,927 | 176 → 178 | 4,487 → 4,673 |
| ScalarString | 21206 ns (21184–21420) | 28608 ns (28506–30171) | 55,780 → 55,824 | 179 → 181 | 4,596 → 4,782 |
| ScalarBool | 21130 ns (21115–21133) | 27839 ns (27826–29977) | 55,171 → 55,215 | 171 → 173 | 4,612 → 4,798 |
| ObjectField | 34600 ns (34432–34697) | 46381 ns (45227–46400) | 173,127 → 173,167 | 207 → 209 | 28,957 → 29,143 |
| RepeatedObject | 53252 ns (53212–53758) | 56103 ns (55917–56750) | 273,846 → 215,214 | 416 → 355 | 41,460 → 33,716 |
| ArrayPredicate | 55657 ns (55520–55925) | 67172 ns (60202–67446) | 341,700 → 341,725 | 270 → 272 | 30,002 → 30,188 |
| ConstantObject | 179794 ns (179603–180311) | 184866 ns (182001–211794) | 1,492,110 → 1,492,170 | 443 → 447 | 119,753 → 120,129 |
| ConstantInvalidNested | 180842 ns (179610–180871) | 197330 ns (186629–197456) | 1,492,122 → 1,492,196 | 443 → 447 | 119,753 → 120,129 |
| RepeatedConstants | 344283 ns (337848–353356) | 342103 ns (338851–356236) | 2,963,476 → 2,926,102 | 809 → 783 | 239,556 → 236,157 |

### PostgreSQL execution

Planning and execution are reported separately. `Matched` is the result count
for both baseline and candidate.

| Rows / workload | Baseline planning ms (min–max) | Candidate planning ms (min–max) | Baseline execution ms (min–max) | Candidate execution ms (min–max) | Plan nodes | SQL bytes | Matched |
|:--|--:|--:|--:|--:|--:|--:|--:|
| 100Rows/ScalarNumber | 0.281 ms (0.262–0.296) | 0.263 ms (0.252–0.263) | 0.492 ms (0.487–0.54) | 0.354 ms (0.348–0.357) | 36 → 36 | 4,487 → 4,673 | 90 |
| 100Rows/ScalarString | 0.289 ms (0.282–0.29) | 0.281 ms (0.258–0.297) | 0.314 ms (0.294–0.342) | 0.19 ms (0.172–0.212) | 36 → 36 | 4,596 → 4,782 | 45 |
| 100Rows/ScalarBool | 0.291 ms (0.245–0.301) | 0.291 ms (0.277–0.415) | 0.292 ms (0.292–0.312) | 0.192 ms (0.174–0.192) | 36 → 36 | 4,612 → 4,798 | 45 |
| 100Rows/ObjectField | 0.901 ms (0.855–0.943) | 0.865 ms (0.846–0.93) | 4.079 ms (4.019–4.292) | 2.73 ms (2.61–2.831) | 132 → 132 | 28,957 → 29,143 | 74 |
| 100Rows/RepeatedObject | 1.573 ms (1.433–1.586) | 1.189 ms (1.126–1.598) | 11.66 ms (11.63–11.92) | 3.256 ms (3.19–3.423) | 220 → 164 | 41,460 → 33,716 | 38 |
| 100Rows/ArrayPredicate | 1.156 ms (1.141–1.238) | 1.197 ms (1.105–1.215) | 1.2 ms (1.094–1.211) | 0.96 ms (0.931–1.005) | 144 → 144 | 30,002 → 30,188 | 77 |
| 100Rows/ConstantObject | 4.066 ms (3.967–4.138) | 4.223 ms (4.014–4.943) | 13.24 ms (12.8–13.34) | 10.38 ms (10.36–10.46) | 499 → 499 | 119,753 → 120,129 | 1 |
| 100Rows/ConstantInvalidNested | 4.037 ms (4.009–4.049) | 4.486 ms (4.077–4.824) | 4.656 ms (4.647–4.827) | 3.521 ms (3.213–3.655) | 499 → 499 | 119,753 → 120,129 | 0 |
| 100Rows/RepeatedConstants | 7.887 ms (7.761–8.616) | 8.404 ms (8.323–10.73) | 2.976 ms (2.788–2.988) | 1.715 ms (1.64–1.846) | 998 → 970 | 239,556 → 236,157 | 90 |
| 1000Rows/ScalarNumber | 0.266 ms (0.246–0.274) | 0.289 ms (0.256–0.293) | 3.827 ms (3.796–4.016) | 2.919 ms (2.873–2.964) | 36 → 36 | 4,487 → 4,673 | 900 |
| 1000Rows/ScalarString | 0.286 ms (0.261–0.296) | 0.288 ms (0.287–0.327) | 2.641 ms (2.611–2.725) | 1.262 ms (1.249–1.276) | 36 → 36 | 4,596 → 4,782 | 450 |
| 1000Rows/ScalarBool | 0.28 ms (0.276–0.284) | 0.274 ms (0.273–0.281) | 2.679 ms (2.653–2.685) | 1.291 ms (1.284–1.317) | 36 → 36 | 4,612 → 4,798 | 450 |
| 1000Rows/ObjectField | 0.84 ms (0.824–0.846) | 1.019 ms (0.998–1.098) | 36.35 ms (36.08–36.57) | 25.09 ms (24.59–25.16) | 132 → 132 | 28,957 → 29,143 | 746 |
| 1000Rows/RepeatedObject | 2.012 ms (1.635–2.245) | 1.205 ms (1.162–1.219) | 112.7 ms (112.6–117.6) | 29.64 ms (28.93–29.81) | 220 → 164 | 41,460 → 33,716 | 368 |
| 1000Rows/ArrayPredicate | 1.263 ms (1.161–1.323) | 1.21 ms (1.089–1.592) | 10.7 ms (10.64–11.08) | 8.319 ms (8.113–8.751) | 144 → 144 | 30,002 → 30,188 | 772 |
| 1000Rows/ConstantObject | 4.437 ms (4.234–4.458) | 4.44 ms (4.386–4.446) | 124.4 ms (124.3–125.9) | 93.6 ms (93.2–94.64) | 499 → 499 | 119,753 → 120,129 | 9 |
| 1000Rows/ConstantInvalidNested | 4.611 ms (4.561–4.75) | 4.644 ms (4.517–4.805) | 42.07 ms (41.38–42.84) | 28.7 ms (28.55–29.11) | 499 → 499 | 119,753 → 120,129 | 0 |
| 1000Rows/RepeatedConstants | 8.967 ms (8.298–9.061) | 8.332 ms (8.275–8.666) | 16.53 ms (15.93–16.62) | 6.541 ms (6.418–6.815) | 998 → 970 | 239,556 → 236,157 | 900 |

Build time increased in eight of nine workload medians. `ScalarNumber` rose
from 21.201 to 31.930 µs/op (about 51%); `RepeatedObject` rose from 53.252 to
56.103 µs/op even though its allocation volume and SQL size fell.
`RepeatedConstants` had a slightly lower Build median. A final baseline and
candidate sample differed from an earlier candidate run, so these Build
figures describe the captured run rather than isolate code-only cost.

Across the sampled PostgreSQL executions, candidate medians were lower for all
nine query shapes at both row counts. The largest difference was
`RepeatedObject` at 1,000 rows: 112.700 to 29.640 ms/op, SQL 41,460 to 33,716
bytes, and 220 to 164 plan nodes. These measurements describe one local host,
PostgreSQL version, fixture, and three-sample run; they do not predict other
workloads or isolate all run variability.

## Correctness and tradeoffs

All benchmark samples matched local HFF result IDs, including malformed
documents and nested invalid numeric values. Full PostgreSQL integration passed
in 268.095 seconds; project formatting, race, vet, parser generation, examples,
WebAssembly, and site checks also passed. PostgreSQL 16+ JSON syntax, Unicode,
and unique-key checks remain in force. Reuse is limited to the exact
SQL receiver expression in a projection scope; each call retains its own
presence state, and callback/shadowed-alias scopes remain isolated. SQL
execution does not enforce local `MaxJSONDepth` or step budgets. Decoded JSON
strings and object keys must contain no NUL; see the
[PostgreSQL `fromjson()` contract](../backends/postgresql/json.md#parse-json-text-with-fromjson).

## Decision and limits

The candidate reduced generated SQL and measured PostgreSQL execution time
most clearly for repeated object access, while Build time rose in eight of
nine workload medians and SQL grew slightly for several single-use queries.
The results do not establish a general speedup or a resource bound. Full
document validation remains necessary for every root, including unused nested
values, so an invalid document cannot be accepted by pruning its parsed fields.
These results do not show that all database work is cached.
