# Finite-limit review

HFF exposes finite resource limits as service defaults. Their current values
and counting contracts are listed in the [local backend configuration
guide](../backends/local/configuration.md#limits). This
page tracks benchmark evidence for reviewing those defaults; it does not turn
operation counts into latency guarantees.

## Reproduce the selected boundary workloads

Run the benchmark with Go 1.27.1 and allocation reporting:

```sh
go test -count=5 -benchtime=100ms -run '^$' -bench '^BenchmarkDefaultLimitWorkloads$' -benchmem ./engine
```

The query-byte case pads a bare `items` predicate with whitespace to exactly
the configured byte boundary, then adds one byte to verify rejection. It
measures byte scanning rather than a dense syntax tree; the parser and compiler
benchmarks separately cover conjunctions up to 1,000 terms. The input case
validates a root object and list with exactly `MaxInputValues` counted values,
then adds one value to verify rejection. Runtime cases include successful and
aborted evaluations: a nested search over 600 items completes, identity-map
creates 99,999 generated values within the 100,000-value limit, and separate
cases verify `MaxSteps` and `MaxGeneratedValues` aborts. They check the
expected result or typed limit failure. Query construction, record
construction, filter building, and input validation are outside the runtime
timed loops. These cases test selected boundaries and abort paths, not every
limit or application workload.

One five-run baseline on Go 1.27.1, Darwin arm64, Apple M3 Max, with a 100 ms
benchmark time per case produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| Query-byte boundary | 475,281 ns/op | 8,379 B/op | 23 allocs/op |
| Query-byte rejection | 174 ns/op | 120 B/op | 3 allocs/op |
| Input-value boundary | 1,115,028 ns/op | 0 B/op | 1 alloc/op |
| Input-value rejection | 559,828 ns/op | 142 B/op | 4 allocs/op |
| Nested search, 600 items | 10,196,788 ns/op | 539 B/op | 4 allocs/op |
| Step-budget abort | 11,131,892 ns/op | 643 B/op | 6 allocs/op |
| Identity map, 99,999 generated values | 4,968,475 ns/op | 12,970,514 B/op | 33 allocs/op |
| Generated-value abort | 2,362,478 ns/op | 4,768,139 B/op | 50,027 allocs/op |

These measurements describe only the benchmark's records and queries. Compare
them with representative service workloads on the target Go version and
hardware before using them to justify a default. A benchmark result is not a
wall-clock deadline or a performance promise. `B/op` reports cumulative
allocation during a benchmark iteration, not peak or resident memory; the
identity-map case's roughly 13 MB reflects that allocation churn.

## Verify default depth boundaries

Run the engine boundary tests with the race detector:

```sh
go test -race ./engine -run '^TestDefaultDepthBoundaries$'
```

The tests accept and reject the exact configured boundaries for syntax depth,
input depth, callback depth, and JSON parse/serialization depth, and assert the
typed limit errors. They also verify that a deeply nested callback expression
can hit the default syntax-depth limit before its separate callback-depth
limit. The callback-depth cases raise syntax depth to isolate the callback
limit. JSON serialization uses an input-depth allowance of 65 so the JSON
depth limit is the one exercised. These are correctness checks, not latency or
throughput measurements.

## Measure selected depth-limit workloads

The engine benchmark measures completed work at the default syntax, callback,
input, and JSON depth ceilings. The callback case raises `MaxSyntaxDepth` to
512 so it measures `MaxCallbackDepth` independently; under the normal syntax
default, syntax depth can reject deeply nested callbacks first. The input case
times `Validate`; the JSON cases use a previously validated `Input` and time
`MatchInput`.

```sh
go test -count=5 -benchtime=100ms -run '^$' -bench '^BenchmarkDefaultDepthWorkloads$' -benchmem ./engine
```

One five-run baseline on Go 1.27.1, Darwin arm64, Apple M3 Max, with a 100 ms
benchmark time per case produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| Syntax depth 64 | 13,218 ns/op | 38,252 B/op | 280 allocs/op |
| Callback depth 64, syntax limit raised to 512 | 394,374 ns/op | 554,002 B/op | 6,542 allocs/op |
| Input depth 64 | 835 ns/op | 0 B/op | 0 allocs/op |
| JSON `fromjson` depth 64 | 2,796 ns/op | 3,210 B/op | 128 allocs/op |
| JSON `tojson` depth 64 | 1,509 ns/op | 440 B/op | 7 allocs/op |

These cases verify completed operations at selected depth ceilings. Exact
acceptance and rejection immediately around the defaults are checked by
`TestDefaultDepthBoundaries`; neither benchmark is a latency guarantee or
evidence that the defaults fit every workload.

## Verify the compilation-work boundary

Run the compile-step boundary test with the race detector:

```sh
go test -race ./engine -run '^TestDefaultCompileBudgetForRepeatedStructuralChecks$'
```

The test compiles the same shallow, 3,999-byte query containing 1,000 repeated
`x=y` predicates against schemas whose `x` and `y` values each contain a
struct. With 400 integer fields in each operand, compilation completes within
the default `MaxCompileSteps`. With 600 fields, it returns a span-bearing
`LimitError` and no partial program. Raising only `MaxCompileSteps` to
2,000,000 lets that same query and schema compile. The query remains below the
byte ceiling and the syntax is shallow, so this isolates semantic compile
work from query-byte and syntax-depth limits. It is a correctness boundary
check, not a timing measurement or evidence for tuning the default.

## Remaining evidence gaps

Boundary correctness and selected depth-64 workload costs are now measured.
The compile-step test establishes completion/rejection behavior for the
selected repeated structural-check workload, not the cost or behavior of
every query near the default `MaxCompileSteps`; existing compile benchmarks
cover representative query shapes and sizes, not a one-million-step boundary.
Runtime benchmarks include one successful
nested-search shape and one near-limit generated collection, plus explicit
abort cases; they do not cover a broader set of query shapes or service
workloads. A reasoned review of the default values remains necessary.
