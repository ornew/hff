#!/bin/sh
# Exercise mutation fuzzing separately from the deterministic seed suite.
set -eu
cd "$(dirname "$0")/.."

export GOTOOLCHAIN=go1.27.1
test "$(go env GOVERSION)" = go1.27.1
fuzz_time=${HFF_FUZZ_TIME:-15s}
fuzz_parallel=${HFF_FUZZ_PARALLEL:-2}

run_fuzz() {
  go test "$1" -run '^$' -fuzz "^$2$" -fuzztime "$fuzz_time" -parallel "$fuzz_parallel"
}

run_fuzz ./internal/syntax FuzzParseQuery
run_fuzz ./engine FuzzCompile
run_fuzz ./internal/core FuzzInputUTF8
run_fuzz ./internal/core FuzzInputKeyOrder
run_fuzz ./backends/local FuzzLocalMatch
run_fuzz ./backends/local FuzzJSONRoundTrip
run_fuzz ./backends/sql FuzzPostgreSQLBuild
run_fuzz ./playground FuzzBridge
