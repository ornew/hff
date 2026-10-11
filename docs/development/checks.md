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
