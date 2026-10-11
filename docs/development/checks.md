# Development Checks

Run the repository's local verification entry point from the repository root:

```sh
sh hack/check.sh
```

The script requires Go 1.27.1. It runs all Go tests with the race detector and
`go vet`, regenerates the PEGO parser and checks that the generated parser has
no diff, runs every `examples/*/main.go` program, tests and vets the site
module, builds the documentation site (including its local-link checks), and
finishes with `git diff --check`. Parser generation pins PEGO to a revision;
`PEGO_BIN` can be set to a local compatible PEGO executable.

The GitHub Actions workflow in `.github/workflows/check.yml` runs the same
script on pushes and pull requests. It uses the Go 1.27.1 toolchain and only
read access to repository contents. A successful local run does not establish
that the remote workflow has completed; check the workflow result for the
commit under review.

## Run the complete CI check scripts

The main job runs three scripts with Go 1.27.1 and Node.js 24.19.0:

```sh
sh hack/check.sh
sh hack/check-wasm.sh
sh hack/check-fuzz.sh
```

`check.sh` runs native race tests and vet, verifies generated parser freshness,
runs all examples, checks and builds the site, and checks the diff. The
WebAssembly script runs package tests through Node's `js/wasm` executor. The
fuzz script runs all eight bounded parser, compiler, runtime, SQL Build, and
playground bridge fuzz targets for 15 seconds each with two workers by default.
Set `HFF_FUZZ_TIME` or `HFF_FUZZ_PARALLEL` to adjust that run locally.

A separate PostgreSQL job uses the pinned PostgreSQL 18.6 image initialized
with UTF-8 encoding and `C` locale. It runs race-enabled tagged SQL integration
tests and tagged vet:

```sh
go test -race -tags=integration ./backends/sql
go vet -tags=integration ./backends/sql
```

The workflow file documents the jobs; it does not prove a hosted run succeeded.
Check GitHub Actions for the commit under review, and distinguish local checks
from remote results.

## Test Go packages on WebAssembly

Run the existing Go package tests for `js/wasm` through Node.js with:

```sh
sh hack/check-wasm.sh
```

The script requires Go 1.27.1 and Node.js. If `node` is not on `PATH`, set
`HFF_WASM_NODE` to the absolute path of the Node.js executable. Arguments after
the script replace the default `./...` package pattern; for example:

```sh
sh hack/check-wasm.sh -count=1 ./engine ./backends/local
```

This runs Go tests using Node's WebAssembly executor and verifies package
compatibility with `js/wasm`. It does not exercise the private playground
bridge or establish a distribution contract or browser UI. See the
[private bridge guide](wasm-bridge.md) for its separate native/WASM parity
smoke test.

## Run focused benchmarks

To reproduce the local validation, reusable-match, and compilation benchmark
samples, run:

```sh
go test -run '^$' -bench . -benchmem ./engine ./backends/local
```

These measurements depend on the Go version, hardware, schema, and query shape.
Treat short benchmark runs as comparisons for the measured cases, not as
performance guarantees or evidence for changing default limits.

The parser benchmark covers empty and representative service queries, type
guards, nested callbacks, Unicode, conjunctions of 10/100/1,000 terms, and
parenthesis depths 4/16. It measures `ParseQuery`, including source preflight
and tree checks, but excludes schema inference, capability checks, and backend
construction.

```sh
go test ./internal/syntax -run '^$' -bench '^BenchmarkParseQuery$' -benchmem
```

The compile-step boundary test separately checks that a shallow query over a
large schema completes or returns a span-bearing limit error according to
`MaxCompileSteps`, with no partial program. It is a correctness check rather
than a benchmark; see the [finite-limit review](finite-limits.md#verify-the-compilation-work-boundary).

```sh
go test -race ./engine -run '^TestDefaultCompileBudgetForRepeatedStructuralChecks$'
```

Measure `Validate` and `Match` around the default `MaxInputValues` ceiling with
a flat list that the predicate does not inspect:

```sh
go test -benchtime=100ms -count=3 -run '^$' -bench '^BenchmarkDefaultInputValueCeiling$' -benchmem ./engine
```

The [finite-limit review](finite-limits.md) records the input shape, exact
value counts, measured sample, and scope limits.

To measure a deliberately repeated structural-check workload close to the
default compile-work ceiling, run:

```sh
go test -benchtime=3x -count=3 -run '^$' -bench '^BenchmarkDefaultCompileWorkCeiling$' -benchmem ./engine
```

The finite-limit review records the schema shape, calibrated step boundary,
sample costs, and remaining evidence gaps. This is one controlled workload,
not a service performance guarantee.

The collection benchmark in [`collections_bench_test.go`](https://github.com/ornew/hff/blob/main/backends/local/collections_bench_test.go)
measures `MatchInput` on one-, 100-, and 1,000-element inputs for `any` first,
last, and miss cases, `all`, `map`, `fold`, `sort`, and nested search. Input
validation, compilation, and building are outside the measured loop. At 1,000
elements, nested search verifies that the finite default `MaxSteps` budget
returns a limit error instead of disabling the limit.

Run this benchmark alone with allocation reporting:

```sh
go test ./backends/local -run '^$' -bench '^BenchmarkCollections$' -benchmem
```

The record-matching benchmark compares `Match` with `MatchInput` for scalar,
service-regex, `any` first/last, and `all` queries over records with one, 100,
 and 1,000 struct items. Compilation, filter construction, and input validation
are outside the timed loops. `Match` includes per-call validation of the entire
record; `MatchInput` reuses a previously validated immutable input. The
benchmark measures local evaluation and allocations, not a universal speedup.

```sh
go test ./backends/local -run '^$' -bench '^BenchmarkRecordMatching$' -benchmem
```

The finite-limit benchmark measures successful and rejected query-byte and
input-value boundaries, plus completed and aborted local step-budget and
generated-value workloads. Compilation, record construction, filter building,
and input validation for the runtime cases are outside the measured loops. It
checks typed limit errors and does not treat operation counts as wall-clock
deadlines. See the
[finite-limit review](finite-limits.md) for its measured scope and remaining gaps.

```sh
go test -count=5 -benchtime=100ms -run '^$' -bench '^BenchmarkDefaultLimitWorkloads$' -benchmem ./engine
```

The depth-limit benchmark measures successful parsing/compilation, input
validation, and JSON operations at selected default depth ceilings. It raises
the syntax-depth limit in the callback case to isolate callback depth. Exact
boundary rejection is covered separately by `TestDefaultDepthBoundaries`.

```sh
go test -count=5 -benchtime=100ms -run '^$' -bench '^BenchmarkDefaultDepthWorkloads$' -benchmem ./engine
```

The PostgreSQL structured-build benchmark measures pure translation for
composite, JSON, and JSONB bindings with scalar, `any()`, and `foldl()` queries.
It reports Build time and allocations together with generated SQL bytes and
parameter count; it excludes database planning and execution. The short local
run is a baseline for these cases, not a performance guarantee or a basis for
changing default limits. Compare results with the same Go version, hardware,
and query shapes.

```sh
go test ./backends/sql -run '^$' -bench '^BenchmarkPostgreSQLStructuredBuild$' -benchmem
```

## Fuzz local matching

The local matcher fuzz target compiles a query, builds a filter, and generates
records. It compares `Match` with `Validate` followed by `MatchInput`: completed
results must agree, and when either path aborts, both paths must return an HFF
error with the same error code. The retained seeds include base64 and temporal
methods, Unicode regular expressions, and nested callbacks. Generated host
values are bounded to 4 KiB and include additional boolean, floating-point,
byte, duration, timestamp, and string-key lookup cases.

Run the target for 30 seconds with two workers:

```sh
go test ./backends/local -run '^$' -fuzz '^FuzzLocalMatch$' -fuzztime=30s -parallel=2
```

## Fuzz input UTF-8 validation

This target compares HFF's chunked UTF-8 validation with Go's standard UTF-8
validator across valid, malformed, and chunk-boundary inputs. Separate race
tests cancel during a long scan of a host string or map key and verify that no
partial input is returned and the cancellation cause is preserved. The local
JSON decoder uses the same chunked validator before token scanning, allowing
cancellation before a late invalid UTF-8 suffix without double-charging input
bytes; the standard-library JSON string decoder itself remains uninterruptible.

```sh
go test ./internal/core -run '^$' -fuzz '^FuzzInputUTF8$' -fuzztime=30s -parallel=2
```

Check cancellation during JSON input UTF-8 admission with:

```sh
go test -race ./backends/local -run '^TestJSONCancellationBeforeLateInvalidUTF8$'
```

Query UTF-8 admission uses the same chunked validator. Its invalid-input
location rescan remains cancellation-aware and preserves source byte spans;
the generated `ParseAST` call is still an uninterruptible boundary.

```sh
go test -race ./internal/syntax -run '^TestQueryUTF8AdmissionCancellation$'
```

The map-key ordering target compares HFF's context-aware merge order with Go's
byte-lexical `sort.Strings` result, including long common prefixes and malformed
UTF-8 byte sequences.

```sh
go test ./internal/core -run '^$' -fuzz '^FuzzInputKeyOrder$' -fuzztime=30s -parallel=2
```

## Run shared semantic conformance cases

The shared fixture in [`cases.go`](https://github.com/ornew/hff/blob/main/internal/conformance/cases.go)
currently contains 43 cases. The parser, compiler, and local evaluator use the
same sources through [`shared_conformance_test.go`](https://github.com/ornew/hff/blob/main/internal/syntax/shared_conformance_test.go),
[`shared_conformance_test.go`](https://github.com/ornew/hff/blob/main/engine/shared_conformance_test.go),
and [`shared_conformance_test.go`](https://github.com/ornew/hff/blob/main/backends/local/shared_conformance_test.go).
These tests assert observable parse/compile errors and match results rather
than internal IR structure.

Run all three suites with the race detector:

```sh
go test -race ./internal/syntax ./engine ./backends/local -run '^TestShared'
```

The parser and compiler fuzz targets also seed from every shared conformance
source. Adding a case to the fixture therefore adds it to both fuzz corpora.
Fuzzing checks parser/compiler robustness, typed diagnostics, source-bounded
spans, and valid compiled programs; it does not decide each case's expected
semantics. The shared conformance tests above remain the source of truth for
those expected outcomes. The compiler target uses the fixture's schema,
including `Any`, union, and collection fields.

Run either target for 30 seconds with two workers:

```sh
go test ./internal/syntax -run '^$' -fuzz '^FuzzParseQuery$' -fuzztime=30s -parallel=2
go test ./engine -run '^$' -fuzz '^FuzzCompile$' -fuzztime=30s -parallel=2
```

The shared fixture covers the parser/compiler/local contract. SQL tests classify
every shared case by its expected compile outcome and PostgreSQL Build support;
adding an unclassified case fails the tests. Execution parity currently covers
seven supported cases across 14 query/record pairs in each of the native,
JSON, and JSONB binding forms (42 comparisons total), checking fixture
expectations against both local matches and PostgreSQL results. The two shared
regex cases produce typed unsupported-backend errors for all three forms. The
remaining classifications do not claim PostgreSQL execution parity, and broader
SQL semantics, extension evaluators, and a comprehensive runtime budget matrix
remain open.

Run the SQL adapter against PostgreSQL:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
go test -tags=integration ./backends/sql -run '^TestPostgreSQLSharedScalarConformance$'
```

JSON parsing and serialization have focused package tests and a round-trip
fuzz target.

## Fuzz and benchmark JSON conversion

The strict JSON codec fuzz target checks round trips and malformed-input
handling. The benchmarks measure codec work at several object sizes; they do
not establish a performance guarantee or justify changing default limits.

```sh
go test ./backends/local -run '^$' -fuzz '^FuzzJSONRoundTrip$' -fuzztime=10s -parallel=2
go test ./backends/local -run '^$' -bench JSON -benchmem
```

## Test PostgreSQL integration

The SQL builder fuzz target repeatedly builds the same typed plan, including
native composite and JSON/JSONB bindings. It checks deterministic SQL and
arguments, typed Build errors with source spans, and absence of panics. It does
not execute SQL or establish semantic parity; the tagged PostgreSQL integration
suite below compares SQL results with local HFF evaluation.

Run the builder target for 30 seconds with two workers:

```sh
go test ./backends/sql -run '^$' -fuzz '^FuzzPostgreSQLBuild$' -fuzztime=30s -parallel=2
```

The SQL backend's tagged integration test uses a running PostgreSQL container
through `docker exec` and `psql`. It compares supported SQL predicate results
with local evaluation for the same compiled inputs. Provide a container name or
ID explicitly; the test fails if it is absent rather than silently skipping:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -tags=integration ./backends/sql
```

The ordinary SQL tests need no database. The integration suite currently covers
the implemented PostgreSQL scalar subset; unsupported operations are checked as
Build errors in package tests.

To measure server-side planning and execution for the controlled JSON/JSONB
scan workload, run the dedicated benchmark with a PostgreSQL container:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLJSONExecution$' -benchtime=1x -count=3
```

Set `HFF_POSTGRES_PLAN_DIR` to save each case's first plan and SQL sample. The
fixture setup and Docker/psql transport are excluded from the reported
PostgreSQL `EXPLAIN` times. See the [PostgreSQL execution baseline](postgresql-execution.md)
for workload details and the measured sample; it is not an index or service
performance guarantee.
