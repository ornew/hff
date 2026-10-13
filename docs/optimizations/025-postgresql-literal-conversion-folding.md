# 025. Fold closed literal conversions during PostgreSQL Build

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL expressions with literal-only conversion chains could expand into
many projection barriers and conversion CTEs even though their inputs were
known during SQL construction. For a closed chain of supported built-in
conversions, the translator can resolve the conversion while building the
query and bind its typed result directly in ordinary value positions as well
as predicates. Direct predicate lowering remains subject to its own
eligibility checks.

Folding recognizes only the exact built-in method IDs `string.bool`,
`string.number`, `string.duration`, `string.timestamp`,
`string.frombase64`, `string.frombase64r`, `bytes.base64`, `bytes.base64r`, and
`any.string`, with the input restrictions below. It does not execute arbitrary
extensions as constant evaluators, skip capability or Profile checks, or fold
expressions that read a bound field or parameter. Existing UTF-8 and NUL input
restrictions remain in force. Failed conversions remain HFF null. A syntactic
null-test node remains unchanged even when its literal child is folded.
PostgreSQL Float text parsing and Float arithmetic remain native so the
backend's declared approximate model is preserved.

The accepted literal forms include `string:bool()`, integer-spelled
`string:number()`, `string:duration()`, `string:timestamp()` on a bare RFC 3339
string literal, strict `string:frombase64()` and `string:frombase64r()`,
`bytes:base64()` and `bytes:base64r()`, and `any:string()` for String, Bool,
Int, Null, Bytes, or Timestamp values. Duration and collection formatting
retain their existing kernels. Other chained Timestamp receivers retain their
existing Build rejection.
Float-looking number text containing a decimal point or exponent remains on
PostgreSQL's native input path. Duration parsing reuses the exact core parser;
local evaluation and its charging rules are unchanged.

## Applicability

| Execution profile | Applied / Partial / Not applied | Reason |
|:--|:--|:--|
| Closed literal conversions in ordinary value positions | Applied | The built-in result is resolved during Build; predicate lowering has separate eligibility checks. |
| Integer-spelled `string:number()` | Applied | The conversion result is an Int and can feed the native predicate directly. |
| Float-spelled `string:number()` | Not applied | PostgreSQL performs native Float parsing under its Profile model. |
| Conversion input depends on a bound value or parameter | Not applied | The value is not known during Build and remains a runtime expression. |
| Extension conversion | Not applied | Extensions are not used as constant evaluators; normal Build translation still invokes registered SQL translators. |
| Literal conversion nested under a syntactic null test | Applied | The literal child folds, while the null-test node keeps its established lowering. |

## Reproduction

The baseline is commit
[3c83453](https://github.com/ornew/hff/commit/3c83453a8634a1806ca4ce6c6e78dd12ae58e891).
The schema used nullable columns `age bigint`, `flag boolean`, `name text`,
`blob bytea`, `elapsed bigint` (Duration nanoseconds), and `ratio double
precision`. Each query was compiled with `capabilities.Standard()` and built
for PostgreSQL using a direct binding for each field. The measured filters
were:

| Filter | Baseline WHERE bytes / barriers / recursive CTEs | Folded WHERE bytes / barriers / recursive CTEs | Bound arguments after folding |
|:--|--:|--:|:--|
| `age>"18":number flag="true":bool name=42:string` | 5,834 / 15 / 0 | 243 / 0 / 0 | `18`, `true`, `"42"` |
| `elapsed>"1h":duration` | 2,096 / 5 / 1 | 72 / 0 / 0 | `3600000000000` nanoseconds |
| `blob="YQ==":frombase64` | 720 / 6 / 0 | 76 / 0 / 0 | hex text `"61"` for `bytea` decoding |
| `name:contains(true:string,"YQ==":frombase64:base64)` | 1,288 / 9 / 0 | 172 / 0 / 0 | `"true"`, `"YQ=="` |
| `age:in("18":number,"21":number,"bad":number)` | 23,265 / 45 / 3 | 80 / 0 / 0 | `18`, `21`; the failed conversion remains null and matches no item |
| `ratio>"1.5":number` | 3,440 / 8 / 0 | 3,440 / 8 / 0 | unchanged native Float conversion |

The counts measure `len(q.Where())` in bytes, occurrences of `OFFSET 0`, and
`WITH RECURSIVE` CTEs. Before folding, the first filter binds `"18"`, `"true"`,
and `42`; afterward it binds `18`, `true`, and `"42"` in the same source order.
The query inputs and schema above reconstruct the Build
shapes; focused tests are in `backends/sql/literal_conversion_test.go` and
`backends/sql/literal_conversion_integration_test.go`. The measured shape
script is retained outside Git at `/private/tmp/hff-literal-shape.go`; its
outputs are `/private/tmp/hff-literal-baseline.shape` and
`/private/tmp/hff-literal-candidate.shape`. These are deterministic SQL-shape
observations, not timing samples or a benchmark.

For a plan-shape check, the first filter was prepared against a temporary
`records(age bigint, flag boolean, name text, blob bytea, elapsed bigint,
ratio double precision)` table with three rows:
`(20,true,'42',NULL,NULL,NULL)`, `(18,false,'x',NULL,NULL,NULL)`, and
`(NULL,NULL,NULL,NULL,NULL,NULL)`. `EXPLAIN (COSTS OFF)` without `ANALYZE`
showed 32 plan lines and 14 Nested Loop nodes before folding, and 2 lines and
no Nested Loop nodes after folding. Both plans were sequential scans. No index
or execution-time conclusion follows from this small fixture.

Focused PostgreSQL/local parity covered 810 new query/record comparisons,
including ordinary value positions, null tests, list null searches, and
callbacks. These include 780 scalar/conversion cases and 30 collection cases.
Existing byte, computed-Duration, strict-string-conversion, native stored
Duration, direct-scalar, and native-Float regression controls also passed.
Unit tests, `go vet ./...`, and integration-tagged vet passed; no benchmark or
`EXPLAIN ANALYZE` was run.

## Correctness and tradeoffs

The fold preserves conversion results and failure behavior while moving a
constant result into the query's typed arguments. For direct native scalar
`in()`, a failed conversion is a null needle that cannot match a non-null
scalar, so it can be omitted; a null receiver still propagates null. General
list searches can match null and retain their ordinary lowering. Remaining
SQL parameters retain source order. Numeric Float parsing remains native, and
nonliteral inputs retain runtime lowering. When a failed folded conversion is
passed to a SQL extension, its SQL `NULL` retains the concrete result carrier
type where one exists; the HFF value remains null with presence unchanged.

## Decision and limits

Fold closed chains of the supported built-in method IDs only when all inputs
are compile-time constants. Keep compile-time capability, Profile, extension,
and source-diagnostic checks intact. SQL-size and plan-shape changes do not
establish execution speed.
