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

## Run focused benchmarks

To reproduce the local validation, reusable-match, and compilation benchmark
samples, run:

```sh
go test -run '^$' -bench . -benchmem ./engine ./backends/local
```

These measurements depend on the Go version, hardware, schema, and query shape.
Treat short benchmark runs as comparisons for the measured cases, not as
performance guarantees or evidence for changing default limits.

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

## Fuzz local matching

The local matcher fuzz target compiles a query, builds a filter, and generates
records. It compares `Match` with `Validate` followed by `MatchInput`: completed
results must agree, and when either path aborts, both paths must return an HFF
error with the same error code. The retained seeds include base64 and temporal
methods, Unicode regular expressions, and nested callbacks.

Run the target for 30 seconds with two workers:

```sh
go test ./backends/local -run '^$' -fuzz '^FuzzLocalMatch$' -fuzztime=30s -parallel=2
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

The fixture covers the current parser/compiler/local contract. SQL parity,
extension evaluators, and a comprehensive runtime budget matrix remain outside
its coverage. JSON parsing and serialization have focused package tests and a
round-trip fuzz target.

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
