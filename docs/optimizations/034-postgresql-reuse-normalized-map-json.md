# 034. Reuse normalized JSON from map callbacks

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL `map()` builds JSON values under the callback's inferred stored
type. A later operation could traverse and normalize that generated document
again, even when the exact SQL expression already had a validated
representation. The translator now records a proof for the generated map
output using its SQL expression, native JSON format, and captured stored-type
key. It reuses the proof only after full schema validation.

The proof survives ordinary alias projections and SQL-NULL-to-JSON-null
normalization. It does not pass through guards, type guards, extraction,
casts, or other derived expressions. Presence and Float-loss provenance are
separate, and final canonical serialization remains on its existing path.
This slice applies to map outputs; it does not establish equivalent proof for
merged fold state, `flatmap()`, or arbitrary constructed containers.

## Applicability

| Value path | Status | Reason |
|:--|:--|:--|
| Fully validated `map()` output with the exact stored type and native JSON representation | Applied | Reuse the established representation for that exact SQL value. |
| Ordinary projection alias or SQL NULL normalized to JSON null | Applied | These preserve the expression's value and representation proof. |
| Guard, type guard, extraction, cast, or other derived value | Not applied | Each derived expression needs its own representation proof. |
| `flatmap()`, merged fold state, or arbitrary container output | Not applied | Their output and merged-state representation require separate proof. |
| Presence or Float-loss handling | Not changed | These remain independent of the normalized-JSON proof. |

## Reproduction

The baseline is commit
[d8673fa](https://github.com/ornew/hff/commit/d8673fa04cc055e90849544a0ebd1c4ee324daa6).
The temporary SQL-shape fixture used Go 1.27.1 on Darwin/arm64 and PostgreSQL
18.6 on aarch64;
shape plans used `extra_float_digits=1`. It bound nullable JSON `items` values
as lists of nullable callback values. The tested callback result families
included numeric unions, Struct values, nested maps, Lists, Bytes,
Duration, and Timestamp.

Representative queries were:

```hff
items:map(x=>x):json="[]"
[items:map(x=>x)]:size>0
[items:map(x=>items:map(y=>y))]:size>0
```

The shape fixture covered 13 workloads: two JSON serialization cases, Struct
results, nested maps, List results, Bytes, Duration, and Timestamp,
plus controls for guarded Map output, `flatmap()`, folds, literals, and simple
`map():size`. It counted generated `WHERE` bytes, `OFFSET 0` barriers, and
`json_array_elements` expansion sites. The temporary shape generator and raw
SQL/plans are outside the repository; the rows below preserve their exact
query sources.

The durable [unit tests](https://github.com/ornew/hff/blob/main/backends/sql/json_normalized_test.go)
cover exact proof matching, alias/null preservation, fallback for derived values,
schema checks, and cancellation. The
[PostgreSQL/local integration test](https://github.com/ornew/hff/blob/main/backends/sql/json_normalized_integration_test.go)
checks 11,648 PostgreSQL/local matches on both the baseline and implementation
across JSON and metadata-backed JSONB, `extra_float_digits` 1 and 3, and
nullable/empty/nonempty List inputs. Its stored-type fixtures
cover Int, Float, String, Bool, Bytes, Duration, Timestamp, numeric and
object unions, Lists, Maps, Structs, and Any. The matrix combines 14 query contexts and 16 record rows per stored type.
Records include SQL NULL and JSON null; queries exercise captures, shadowed
callback bindings, guards, nested callbacks, and serialization/access paths. A separate decode-failure test checks 24
PostgreSQL results on both revisions for raw Float spellings `1e-400`,
`-1e-400`, and `1e400` across nested-map, literal, serialization, and equality
contexts at both Float settings.

Run the durable checks with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^(TestNormalizedJSONProof|TestNormalizedMapOutput)' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLNormalizedMap' -count=1
```

## Results

| Element type before nullable wrapping | Query | Baseline bytes / barriers / array expansions | Candidate bytes / barriers / array expansions |
|:--|:--|--:|--:|
| `string\|int\|float` | `items:map(x=>x):json="[]"` | 6,875 / 10 / 3 | 4,924 / 10 / 2 |
| `int\|float` | `items:map(x=>x):json="[]"` | 6,743 / 10 / 3 | 4,862 / 10 / 2 |
| Struct `{i: int, f: float}` | `[items:map(x=>x)]:size>0` | 5,980 / 9 / 2 | 3,531 / 9 / 1 |
| `float` | `[items:map(x=>items:map(y=>y))]:size>0` | 6,369 / 13 / 5 | 4,376 / 13 / 3 |
| `list[float]` | `[items:map(x=>x)]:size>0` | 5,070 / 9 / 4 | 3,080 / 9 / 2 |
| `bytes` | `[items:map(x=>x)]:size>0` | 3,380 / 9 / 2 | 2,378 / 9 / 1 |
| `duration` | `[items:map(x=>x)]:size>0` | 4,926 / 9 / 2 | 4,476 / 9 / 1 |
| `timestamp` | `[items:map(x=>x)]:size>0` | 3,906 / 9 / 2 | 3,461 / 9 / 1 |
| `int\|float` (guard control) | `(items:map(x=>x))!:json="[]"` | 6,881 / 11 / 3 | 6,881 / 11 / 3 |
| `float` (flatmap control) | `[items:flatmap(x=>[x])]:size>0` | 5,739 / 10 / 4 | 5,739 / 10 / 4 |
| `float` (fold control) | `[items:foldl([], (a,x)=>a:concat([x]))]:size>0` | 11,141 / 18 / 8 | 11,141 / 18 / 8 |
| `float` (literal control) | `[items]:size>0` | 2,245 / 5 / 1 | 2,245 / 5 / 1 |
| `float` (size control) | `items:map(x=>x):size>0` | 1,723 / 8 / 1 | 1,723 / 8 / 1 |

Here, array expansions count `json_array_elements` sites. The focused shape
rows reduce those sites but leave their `OFFSET 0` barrier counts unchanged.
Plain `EXPLAIN` on an empty `records(items json)` fixture showed corresponding
reductions in `Function Scan on json_array_elements` nodes: 3 to 2 for numeric
union serialization, 5 to 3 for nested maps, and 4 to 2 for List-valued
callbacks. Result and Nested Loop counts stayed unchanged in these cases
(11/9, 13/12, and 9/8, respectively). This was plain plan inspection without
`ANALYZE`; execution time was not measured. The
11,648-result parity matrix passed on both baseline and implementation, as
did the separate 24-result Float decode-failure check. These results do not
measure timing, allocations, retained memory, or throughput.

## Correctness and tradeoffs

The proof is keyed to the exact expression, JSON format, and captured stored
type, and full stored-schema validation remains in front of reuse. Ordinary
alias projection and SQL-null normalization preserve the proof; extraction,
casts, and guards do not inherit it. The integration matrix exercises
metadata-backed JSONB at both Float settings alongside native JSON. It does
not validate caller-supplied metadata or eliminate its requirements.

The optimization avoids selected repeated traversals. Required input scans,
JSON construction, callback evaluation, projection barriers, and canonical
output serialization remain. Fold state and broader provenance propagation remain separate
questions. No runtime-speed claim follows from these SQL-shape changes.

## Decision and limits

Reuse normalized representation only for the exact fully validated map output
and value-preserving aliases covered here. Keep presence and Float-loss
provenance separate. Do not infer normalized state from JSON text, key order,
or an unrelated derived expression. Fold/merged state, `flatmap()`, and wider
constructed-container reuse require independent schema and provenance proofs.
