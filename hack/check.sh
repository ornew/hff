#!/bin/sh
# Run the repository's development checks, including generated artifacts.
set -eu
cd "$(dirname "$0")/.."

export GOTOOLCHAIN=go1.27.1
test "$(go env GOVERSION)" = go1.27.1

go test -race ./...
go vet ./...
sh hack/generate-parser.sh
git -c core.fsmonitor=false diff --exit-code -- internal/syntax/parser_gen.go

for example in examples/*/main.go; do
  go run "./$(dirname "$example")"
done

(
  cd site
  go test ./...
  go vet ./...
)
sh site/build.sh
git -c core.fsmonitor=false diff --check
