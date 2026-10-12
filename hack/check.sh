#!/bin/sh
# Run the repository's development checks, including generated artifacts.
set -eu
cd "$(dirname "$0")/.."

export GOTOOLCHAIN=go1.27.1
test "$(go env GOVERSION)" = go1.27.1

hff_unformatted_files=$("$(go env GOROOT)/bin/gofmt" -l .)
if [ -n "$hff_unformatted_files" ]; then
  printf '%s\n' "$hff_unformatted_files"
  printf '%s\n' 'Go files must be formatted with gofmt before checks can pass.' >&2
  exit 1
fi

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
