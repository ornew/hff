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

## Measure a compile workload at the default step ceiling

Run the benchmark with Go 1.27.1 and allocation reporting:

```sh
go test -benchtime=3x -count=3 -run '^$' -bench '^BenchmarkDefaultCompileWorkCeiling$' -benchmem ./engine
```

The workload repeats `x=y` 1,000 times in a 3,999-byte, shallow query. It
compares schemas with 496 and 497 integer fields in each operand and disables
`ListEquality`, so compilation must perform the structural permission checks.
The required compile-work boundary is calibrated outside the timed loop with
the public limit: the 496-field case completes with 998,002 required steps
under the default 1,000,000; the 497-field case requires 1,000,002 and returns
a typed limit error.

One baseline on Go 1.27.1, Darwin arm64, Apple M3 Max, with three iterations per
case and three benchmark runs produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| 496 fields, completed | 40.477 ms/op | 88,881,802 B/op | 29,134 allocs/op |
| 497 fields, limit error | 40.412 ms/op | 88,872,080 B/op | 29,130 allocs/op |

The high `B/op` is cumulative allocation for this deliberately repeated
structural-check workload, not peak or resident memory. This one schema/query
shape demonstrates behavior near the default compile-work ceiling; it does
not establish that the default suits every service or query workload.

After reducing repeated field-slice copies and reusing the per-compilation
visited map, the same benchmark produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| 496 fields, completed | 15.321 ms/op | 1,839,253 B/op | 12,123 allocs/op |
| 497 fields, limit error | 15.287 ms/op | 1,917,176 B/op | 12,128 allocs/op |

For this workload, required steps remained exactly 998,002 and 1,000,002. The
optimization does not memoize structural checks or change compile-step
accounting, and the public `Type.Fields()` copy behavior remains unchanged.
This before/after comparison is specific to the benchmark's repeated
structural checks; it does not predict gains for other compiler workloads.

## Measure input admission at the default value ceiling

Run the benchmark with Go 1.27.1 and allocation reporting:

```sh
go test -benchtime=100ms -count=3 -run '^$' -bench '^BenchmarkDefaultInputValueCeiling$' -benchmem ./engine
```

The record has schema `{items: list[int]}` and the query `items:exists`, which
does not inspect individual list elements. The benchmark compares exactly
100,000 values with 100,001 values; the root object and list count toward that
total. Record construction, compilation, and filter building are outside the
timed loop. `Validate` includes its result-compatibility check, while `Match`
includes full input admission before evaluation.

One baseline on Go 1.27.1, Darwin arm64, Apple M3 Max, with a 100 ms benchmark
time and three runs produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| Validate, 100,000 values | 1.183 ms/op | 32 B/op | 2 allocs/op |
| Match, 100,000 values | 1.232 ms/op | 208 B/op | 3 allocs/op |
| Validate, 100,001 values rejected | 583 µs/op | 157 B/op | 5 allocs/op |
| Match, 100,001 values rejected | 561 µs/op | 157 B/op | 5 allocs/op |

This measures input-admission work for one flat list shape, not record
construction, retained input memory, database or transport cost, or every
service workload. It is evidence for this boundary only, not a universal
budget-safety or latency claim.

## Measure a generated-value workload at the default ceiling

Run the local matcher benchmark with Go 1.27.1 and allocation reporting:

```sh
go test -benchtime=100ms -count=3 -run '^$' -bench '^BenchmarkDefaultGeneratedValueCeiling$' -benchmem ./backends/local
```

The query `items:flatmap(x=>[x,x]):size=2*n` creates an intermediate
three-value list for each input item, then a flattened two-entry result and its
outer list. The benchmark compares 19,999 items (99,996 generated values,
completed) with 20,000 items (100,001 values, rejected). Validation,
compilation, and Build happen outside the timed loop; each iteration calls
`MatchInput` on the previously validated input.

One baseline on Go 1.27.1, Darwin arm64, Apple M3 Max, with a 100 ms benchmark
time and three runs produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| 19,999 items, completed | 2.827 ms/op | 6,374,118 B/op | 40,026 allocs/op |
| 20,000 items, limit error | 2.763 ms/op | 6,374,243 B/op | 40,028 allocs/op |

This is one flatmap workload. Allocation volume is cumulative `B/op`, not peak
or resident memory. `MaxGeneratedValues` counts logical HFF values, not bytes
or peak memory. This timing and allocation sample does not describe other
queries or establish a universal limit-safety or latency guarantee.

## Evidence and coverage matrix

The table separates what each limit counts from the workloads checked so far.
Benchmark figures above are single-host samples; they describe those inputs,
not generally acceptable service latency or a reason to change a default.

| Limit | Counting unit | Boundary evidence | Measured cost and remaining coverage |
|:--|:--|:--|:--|
| `MaxQueryBytes` (65,536) | UTF-8 source bytes | Exact byte boundary accepted and the next byte rejected. | Padded and dense 65,536-byte queries plus a 65,537-byte rejection are measured below. This is one schema/query pair, not a representative query corpus. |
| `MaxSyntaxDepth` (64) | Nested syntax constructs | Exact 64/65 accept/reject. | One depth-64 parse/compile shape measured; no broad grammar or large-query combination matrix. |
| `MaxCallbackDepth` (64) | Lexically nested callbacks; root callback is depth 1 | Exact 64/65 tested with syntax depth raised to isolate this limit; normal syntax depth can reject first. | One depth-64 nested callback workload measured with syntax limit raised; no broad callback-body/schema workload. |
| `MaxCompileSteps` (1,000,000) | Semantic compiler visits, including repeated inference and checking work | Repeated structural checks: 400 fields complete, 600 reject, and raising only this limit to 2,000,000 completes. Calibrated case: 998,002 steps complete; 1,000,002 reject. | One deliberately repeated structural-check workload has before/after timing and allocation measurements. Other compiler benchmarks cover common query shapes, not diverse service schemas near the ceiling. |
| `MaxInputDepth` (64) | Host input root is depth 0; each nested value adds one | Exact depth boundary tested. | One depth-64 validation shape measured; no varied wide/deep record corpus. |
| `MaxInputValues` (100,000) | Root, collection containers, entries, and nested values | 100,000 accepted and 100,001 rejected in correctness tests and benchmarks. | One flat `list[int]` admission workload measured. Record construction, retained memory, transport, and nested/wide service records are outside scope. |
| `MaxJSONDepth` (64) | JSON root is depth 1; applies to decoding and serialization | Exact boundary tested for parsing and output. | One depth-64 `fromjson` and `tojson` shape measured; no broad JSON shape or size matrix. |
| `MaxSteps` (1,000,000) | Local runtime work, including evaluated nodes, traversed data, key bytes, and declared-field checks | Selected completion and limit-error workloads exercise nested search and step exhaustion. | One 600-item nested search and one abort workload are measured. No representative mix of application queries or hot-path records near the ceiling. |
| `MaxGeneratedValues` (100,000) | Generated HFF collection containers and entries; logical values, not bytes | 99,999 generated values complete; 100,001 reject. Flatmap also checks 99,996 complete versus 100,001 rejected. | One identity-map and one flatmap shape are measured. `B/op` is cumulative allocation, not peak memory; other collection patterns and retained output sizes are not covered. |

These checks establish counter behavior for the named cases. They do not prove
that defaults are safe for every service, constrain SQL execution, or predict
database planning, transport, peak memory, or caller-specific latency. SQL
backends reject explicit execution-budget requirements they cannot enforce;
local runtime limits do not become database limits.

## Compare padded and dense queries at the byte ceiling

Run the compilation benchmark with Go 1.27.1 and allocation reporting:

```sh
go test -benchtime=100ms -count=3 -run '^$' -bench '^BenchmarkDefaultQueryByteCeiling$' -benchmem ./engine
```

The benchmark compiles against `{x: bool}`. It compares a 65,536-byte source
containing one predicate padded with spaces with a same-size dense source of
32,768 whitespace-conjoined `x` predicates. Both compile under the defaults; a
65,537-byte source is rejected by byte admission before parsing. Engine and
source setup are outside the timed loop.

One Go 1.27.1, Darwin arm64, Apple M3 Max baseline with a 100 ms benchmark time
and three runs produced these medians:

| Case | Median time | Allocated bytes | Allocations |
|:--|--:|--:|--:|
| Padded, 65,536 bytes | 509 µs/op | 4,946–8,245 B/op across runs | 19 allocs/op |
| Dense, 65,536 bytes | 17.412 ms/op | 38,450,489 B/op | 99,342 allocs/op |
| 65,537-byte rejection | 98 ns/op | 120 B/op | 3 allocs/op |

Generated-parser scratch uses `sync.Pool`, so repeated-run allocation figures
vary with retained scratch and garbage collection. `B/op` is cumulative
allocation, not a fixed per-query memory budget or peak/retained memory. The
rejection result measures this admission path, not general scanning
throughput. This comparison isolates source density at one byte length; it
does not establish a safe maximum query size for other schemas or services.

## Default review outcome

Reviewed 2026-10-12: retain the current finite defaults for the shared HFF
configuration. The intended baseline is a service evaluating human-authored
filters against ordinary schemas. Existing boundary tests and selected workload
measurements show that the configured limits accept and reject their specified
cases; no representative service SLO or workload evidence supports changing
the values. The measurements below are the reason to keep the current limits
as a conservative common starting point, not proof of universal safety.

| Limit group | Reason to retain the current default | Operating boundary |
|:--|:--|:--|
| `MaxQueryBytes` 65,536 and `MaxCompileSteps` 1,000,000 | 64 KiB leaves room for hand-authored filters, while semantic work has its own finite counter for dense expressions. The dense byte-ceiling sample completes, and the structural-check test accepts 998,002 compile steps but rejects 1,000,002. | Dense syntax can consume much more work and cumulative allocation than padded source of the same size. A service can set smaller limits for its query profile. |
| `MaxSyntaxDepth`, `MaxCallbackDepth`, `MaxInputDepth`, and `MaxJSONDepth` 64 | A depth of 64 allows meaningful nested expressions, callbacks, records, and JSON while placing a fixed ceiling on recursive shapes. Exact 64/65 boundaries are tested; selected depth-64 work completes. | Syntax depth can reject a nested callback before the separate callback limit. The callback benchmark raises syntax depth only to isolate the callback counter. |
| `MaxInputValues` 100,000 | The value count bounds records independently of what a query reads. The exact boundary and one flat-list admission workload are verified. | This is a logical-value limit, not a record-byte or retained-memory limit; services should lower it when input size or latency targets require that. |
| `MaxSteps` 1,000,000 and `MaxGeneratedValues` 100,000 | Runtime work and collection fan-out have separate finite limits. Selected nested-search and near-limit collection workloads complete, while abort cases return typed limit errors. | Generated-value counts do not bound bytes or peak memory. The measured workloads do not establish a latency or memory guarantee at the ceiling. SQL execution is caller-managed and does not inherit local runtime budgets. |

The benchmarks cover selected boundary shapes rather than a distribution of
production filters, schemas, or records. They do not establish universal
latency, peak-memory, or service-SLO guarantees. Applications should tune
limits against their own input contracts and SLOs; future representative
workload evidence may justify revisiting these defaults. No additional synthetic
benchmark is required merely to retain the present shared defaults.
