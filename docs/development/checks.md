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
JSON conversion methods, extension evaluators, and a comprehensive runtime
budget matrix remain outside its coverage.
