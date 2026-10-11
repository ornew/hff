#!/bin/sh
# Regenerate the standalone typed parser from HFF's authoritative grammar.
set -eu
cd "$(dirname "$0")/.."
pego_version=ee3b6d2344d074504aa198d08b591d9788c75ecf
if [ -n "${PEGO_BIN:-}" ]; then
  "$PEGO_BIN" gen -g internal/syntax/hff.pego -pkg syntax -types -nodoc -o internal/syntax/parser_gen.go
else
  go run "github.com/ornew/pego/cmd/pego@$pego_version" gen -g internal/syntax/hff.pego -pkg syntax -types -nodoc -o internal/syntax/parser_gen.go
fi
