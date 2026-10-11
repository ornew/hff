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
then adds one value to verify rejection. The runtime cases abort local
evaluations with `MaxSteps` and `MaxGeneratedValues` errors. They check the
expected result or typed limit failure. Query construction, record
construction, and runtime setup are outside the timed loops. These cases test
selected boundaries and abort paths, not every limit or application workload.

One five-run baseline on Go 1.27.1, Darwin arm64, Apple M3 Max, with a 100 ms
benchmark time per case produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| Query-byte boundary | 475,281 ns/op | 8,379 B/op | 23 allocs/op |
| Query-byte rejection | 174 ns/op | 120 B/op | 3 allocs/op |
| Input-value boundary | 1,115,028 ns/op | 0 B/op | 1 alloc/op |
| Input-value rejection | 559,828 ns/op | 142 B/op | 4 allocs/op |
| Step-budget abort | 11,131,892 ns/op | 643 B/op | 6 allocs/op |
| Generated-value abort | 2,362,478 ns/op | 4,768,139 B/op | 50,027 allocs/op |

These measurements describe only the benchmark's records and queries. Compare
them with representative service workloads on the target Go version and
hardware before using them to justify a default. A benchmark result is not a
wall-clock deadline or a performance promise.

## Remaining evidence gaps

The boundary benchmark does not yet exercise adversarial depth at the default
`MaxInputDepth`, `MaxSyntaxDepth`, `MaxCallbackDepth`, or `MaxJSONDepth`. It also
does not establish behavior near the default `MaxCompileSteps`; existing
compile benchmarks cover representative query shapes and sizes, not a
one-million-step boundary. The runtime abort cases do not measure successful
work close to the `MaxSteps` or `MaxGeneratedValues` ceilings. Broader service
workloads and a reasoned review of those limit values remain necessary.
