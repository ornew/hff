# 029. Keep single-family JSON arithmetic results in typed SQL

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL's general JSON arithmetic lowering serializes a computed numeric
result to JSON and decodes it again to recover its HFF type. When the compiled
result has exactly one numeric subtype after excluding Null, and every reachable
branch returns that subtype, the translator can instead return a typed SQL
`CASE` result directly:
`bigint` for Int or `double precision` for Float. This removes the result
serialization and decode while retaining the existing arithmetic kernels.

Representation and JSONB metadata checks still run before type-guard narrowing.
Original operand presence, typed SQL-null carriers, callback scopes and
parameter order are unchanged. Int overflow and promotion remain intact; Int
division still returns Float. Float behavior follows PostgreSQL's native Float
Profile, including its checked extreme-value fallback. Nullable Int and Float
results use the same typed path. Null-only and Never results keep their existing
untyped null carrier; multiple numeric result families retain the JSON path.

## Applicability

| Compiled result and operation | Status | Reason |
|:--|:--|:--|
| Single-family Int result, such as `i+2` | Applied | Reachable branches return Int and can use a typed `bigint` result. |
| Single-family Float result, such as `f+2.0`, `i/2`, or `f:round` | Applied | Reachable branches return Float and can use a typed `double precision` result. |
| Mixed Int/Float operands with one Float result, such as `i+f` | Applied | Required promotion remains; the computed result stays typed Float. |
| Guarded Any whose compiled result is one numeric family | Applied | Existing storage and metadata checks run before the guard narrows branches. |
| Any or a union with multiple possible numeric result families | Not applied | Runtime result subtype still requires the JSON encode/decode path. |
| Nullable Int or Float result | Applied | The typed SQL null fallback preserves null without encoding it as JSON. |
| Null-only or Never result | Not applied | The existing untyped null carrier is preserved. |

## Reproduction

The SQL-shape baseline is commit
[4ebbec1](https://github.com/ornew/hff/commit/4ebbec1849eef7862cf876ded16edcb88af1e1b3).
The fixture used Go 1.27.1 and PostgreSQL 18.6 with
`extra_float_digits=1`. Its schema declared `i` as `String|Int|Null`, `f` as
`String|Float|Null`, `both` as their union, `data` as `Any`, `items` as
`List<String|Int|Null>`, `guard` as nullable Int, and `name` as String. The
`i`, `f`, `both`, `data`, and `items` paths were bound to matching JSON
columns; `guard` and `name` were native columns. The fixture did not include a
`floats` field. All queries were built for the PostgreSQL dialect under the
PostgreSQL Float Profile.

Representative filters were:

```hff
i+2>0
f+2.0>0
i+f>0
i/2>0
-f>0
f:round>0
(data:[string|int]+2):type="int"
```

Durable correctness coverage is in the
[unit tests](https://github.com/ornew/hff/blob/main/backends/sql/typed_numeric_result_test.go)
and [PostgreSQL integration tests](https://github.com/ornew/hff/blob/main/backends/sql/typed_numeric_result_integration_test.go).
The SQL-shape numbers came from a separate temporary local generator; its source
and raw outputs are not part of the repository. The table reports generated
`WHERE` bytes and
`OFFSET 0` barriers. Plain `EXPLAIN` was also inspected; no `EXPLAIN ANALYZE`,
benchmark, or runtime measurement was collected.

Run the focused correctness fixtures with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^Test(TypedNumeric|NumericBranch)' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQL(TypedNumeric|NumericBranchPruning)' -count=1
```

## Results

| Filter | Baseline bytes / barriers | Candidate bytes / barriers |
|:--|--:|--:|
| `i+2>0` | 1,065 / 6 | 888 / 5 |
| `f+2.0>0` | 5,454 / 15 | 2,118 / 9 |
| `i+f>0` | 5,922 / 17 | 2,585 / 11 |
| `i/2>0` | 2,811 / 7 | 1,347 / 6 |
| `(-f)>0` | 3,151 / 7 | 1,337 / 6 |
| `f:round>0` | 3,163 / 7 | 1,339 / 6 |
| `(data:[string\|int]+2):type="int"` | 1,761 / 9 | 1,584 / 8 |
| `(both+2):type="int"` control | 5,922 / 15 | 5,922 / 15 |
| `(data+2):type="int"` Any control | 5,922 / 15 | 5,922 / 15 |
| `name="Alice"` scalar control | 55 / 0 | 55 / 0 |

The plain `EXPLAIN` fixture showed `f+2.0>0` shrinking from 15 to 9 `Result` nodes
and 6 to 1 `Subquery Scan`; `i+f` changed from 17 to 11 `Result` nodes and 6
to 1 `Subquery Scan`. `i+2` changed from 6 to 5 `Result` nodes without a
`Subquery Scan`. These are fixture-specific plan-shape observations, not
runtime-speed evidence.

## Correctness and tradeoffs

The baseline and candidate both passed the 4,064-case JSON/JSONB parity
matrix, 36 SQL-null carrier observations, and 192 new boundary expectations.
The boundary cases cover JSON and metadata-backed JSONB, SQL and JSON null,
guards, wrong-category operands, Int limits and overflow, division by zero,
PostgreSQL subnormal behavior, signed-zero arithmetic and rounding, and
`extra_float_digits` settings 1 and 3. Existing rounding, native Float,
presence, callback-scope and schema-dispatch controls passed as well. Unit
tests, vet, integration-tagged vet, race checks and independent review passed.

## Decision and limits

Return a native typed numeric result only when static analysis proves one
non-null numeric family and all reachable operation branches agree on it.
Keep the JSON result path for multiple numeric subtypes and preserve the
existing Null-only and Never carriers. SQL-size and plan-shape
reductions do not establish execution speed.
