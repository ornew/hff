# 031. Reuse constructed primitive JSON in callbacks

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL `map()` and `foldl()`/`foldr()` store callback results in JSON state.
When a callback constructs a native SQL scalar, the previous path serialized
that value to JSON and immediately decoded and normalized it again using the
inferred stored type. The translator can avoid this second conversion when the
complete stored schema has been checked and the value is a native SQL carrier
whose kind matches one primitive family, optionally unioned with Null.

The eligible carriers are Bool, Int, Float, String, Bytes, Duration, and
Timestamp. SQL `NULL` is coalesced to JSON `null` for the state value, while
presence and no-value remain separate. Float-loss tracking and representability
checks are unchanged. The optimization does not apply when the expression is
already a JSON carrier, or when its stored type admits multiple families or a
container. `flatmap()` still normalizes its list result; `Any` and union
controls keep the general path.

## Applicability

| Constructed value | Status | Reason |
|:--|:--|:--|
| One primitive family, optionally nullable, matching the SQL carrier in `map()` | Applied | Encode the native value into JSON state without parsing and normalizing that new scalar again. |
| One primitive family, optionally nullable, matching the SQL carrier in a fold seed or callback result | Applied | Keep the existing typed scalar's representation while building recursive JSON state. |
| Existing JSON carrier, `Any`, or a multi-family union | Not applied | Preserve schema-guided normalization and representation recovery. |
| Container result or `flatmap()` callback result | Not applied | Preserve collection and list-shape normalization. |
| Nonmatching carrier or unsupported stored schema | Not applied | Keep the ordinary checks and fallback behavior. |

## Reproduction

The SQL-shape baseline is commit
[d88a304](https://github.com/ornew/hff/commit/d88a304dc0a197250c1629895ca3d50505c60ad6).
The SQL-shape fixture used Go 1.27.1 and PostgreSQL 18.6 on aarch64 with
`extra_float_digits=1`. Its `items` column was native JSON with a nullable list
of nullable values; `needle` was nullable native JSON. The 14 shape cases
cover seven primitive map outputs, Int/Float/Bytes folds, and four controls:
`Any`, `Int|Float`, `List<Int>`, and `flatmap()`. The separate integration
fixture included a `List<Float|Null>` container profile, added nullable
`guard.value`, and bound the fields as both native JSON and metadata-backed
JSONB. It ran with `extra_float_digits` set to 1 and 3. Records exercised SQL
NULL and JSON null, empty and nonempty lists, guards, captures, shadowed
parameters, nested callbacks, extreme numeric values, and the HFF Timestamp
year boundaries.

Representative queries were:

```hff
items:map(x=>x+2.0):size>0
items:map(x=>guard!.value):exists
items:foldl(0.0,(a,x)=>a+x)>0
items:foldl(needle,(a,x)=>a)=needle
items:foldr(needle,(x,a)=>a)=needle
```

The map cases end in `:size>0`, numeric folds end in `>0`, and the Bytes fold
ends in `=needle`, as shown. Both shape bindings are nullable native JSON.

The durable fixtures are the
[unit tests](https://github.com/ornew/hff/blob/main/backends/sql/json_constructed_test.go)
and [PostgreSQL integration test](https://github.com/ornew/hff/blob/main/backends/sql/json_constructed_integration_test.go).
The same integration fixture produced 8,960 local/PostgreSQL result matches
against both the baseline and the implementation. Raw SQL, plain plans, and
the temporary SQL-shape generator were kept outside the repository. No timing
benchmark or `EXPLAIN ANALYZE` was run; hardware was not recorded because the
comparison concerns generated SQL shape, not execution speed.

Run the durable checks with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestComputedJSONDocument' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLConstructedCallbackJSONMatchesLocal$' -count=1
```

## Results

| Workload | Baseline bytes / `pg_input_is_valid` / barriers | Candidate bytes / `pg_input_is_valid` / barriers |
|:--|--:|--:|
| Int `map(x=>x+1)` | 1,863 / 0 / 10 | 1,656 / 0 / 10 |
| Float `map(x=>x+2.0)` | 7,207 / 8 / 12 | 2,580 / 2 / 12 |
| String `map(x=>x:lower)` | 1,609 / 0 / 9 | 1,448 / 0 / 9 |
| Bool `map(x=>x:not)` | 1,584 / 0 / 9 | 1,422 / 0 / 9 |
| Bytes `map(x=>x)` | 3,307 / 0 / 8 | 2,226 / 0 / 8 |
| Duration `map(x=>x)` | 6,905 / 0 / 8 | 4,323 / 0 / 8 |
| Timestamp `map(x=>x)` | 7,289 / 0 / 8 | 3,309 / 0 / 8 |
| Int `foldl(0,(a,x)=>a+x)` | 2,469 / 0 / 12 | 2,049 / 0 / 12 |
| Float `foldl(0.0,(a,x)=>a+x)` | 14,516 / 16 / 16 | 4,254 / 4 / 16 |
| Bytes `foldl(needle,(a,x)=>a)` | 6,608 / 0 / 10 | 4,437 / 0 / 10 |
| `Any`, numeric-union, list, and `flatmap()` controls | unchanged | unchanged |

The reported shape counts include generated `WHERE` bytes, literal
`pg_input_is_valid()` occurrences, and `OFFSET 0` barriers. In plain plans,
Float map/fold retained one subquery scan and one function scan; the fold also
retained its recursive union. The `Any`, union, list, and `flatmap()` controls
kept their existing scan counts. These observations do not establish lower
planning or execution time.

## Correctness and tradeoffs

The baseline and implementation each passed 8,960 PostgreSQL/local result
comparisons across the listed schema profiles, JSON formats, Float settings,
and edge cases. Unit checks cover nullable and non-nullable native scalar
carriers, schema rejection, retained normalization for JSON carriers and
containers, and cancellation. Unit tests, vet, integration-tagged vet, SQL
race checks, and independent review passed.

The optimization removes repeated scalar conversion work in selected JSON
construction paths, but does not remove JSON state, callback scans, fold
recursion, presence checks, or Float-loss handling. Other callback values
continue through schema-guided normalization. SQL-size reductions are not
runtime-speed evidence.

## Decision and limits

Keep constructed native primitive values in the typed path until their JSON
state representation is emitted. Continue to normalize JSON carriers,
multi-family values, containers, and `flatmap()` results. The wider audit of
repeated parsing, field dispatch, and Float decomposition remains separate.
