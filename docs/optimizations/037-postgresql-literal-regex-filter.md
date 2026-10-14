# 037. Lower literal regex filters on native String columns directly

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Filter predicates using `~`, `!~`, or built-in `string:matches()` on a directly
bound native String column previously passed through nullable value projections.
For an actual literal String pattern, the translator emits a direct PostgreSQL
regex predicate and binds only the translated pattern. It still validates the
original pattern's UTF-8/NUL contract first, then applies the existing conservative
RE2-to-PostgreSQL translation. The supported pattern subset and `COLLATE "C"`
behavior are unchanged. Only the outer filter's logical leaves use this path;
callback bodies retain their existing value projections and scopes.

The direct positive predicate is shaped like:

```sql
("records"."name" IS NOT NULL AND
 "records"."name" COLLATE "C" ~ ($1::text))
```

Here `$1` contains the translated pattern, for example `(?:\A\u0041)` for
the HFF pattern `^A`. The original pattern is not an additional SQL argument.
`!~` uses an explicit non-null mask and the native `!~` operator. This differs
from applying HFF prefix `!` to a positive match predicate, which negates the
whole false-on-null predicate.

Invalid pattern syntax or a null pattern in filter context folds to `FALSE`
without a bound pattern. For a String pattern, UTF-8/NUL validation still
happens before that fold. A well-formed pattern outside the supported subset remains a
source-located `BackendError` when translation is required. Dynamic patterns
are not eligible. Built-in `matches()` is eligible only for a direct native
String receiver and literal String pattern. Engine registration reserves
built-in names; the lowerer also excludes custom declarations defensively.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Direct native String column with literal String pattern in `~` or `!~` filter | Applied | The translated pattern is the only bound argument; no value projection is needed. |
| Direct native String column with literal String pattern in built-in `matches()` filter | Applied | Filter truth is emitted directly while value-context null behavior remains unchanged. |
| Invalid syntax or null literal pattern in filter context | Applied | The result is `FALSE`; the unused pattern argument is omitted. |
| Guarded/JSON/computed receivers, dynamic patterns, or custom declarations | Not applied | Existing value lowering, extension dispatch, or compatibility errors remain responsible for semantics. |
| `matches()` in a value context | Not applied | Nullable results continue through the general lowering. |

## Reproduction

The baseline is commit
[a4ced06](https://github.com/ornew/hff/commit/a4ced06e1a2518b576646fb47635d5d5694f7858).
The fixture used Go 1.27.1 on Darwin/arm64 and PostgreSQL 18.6 on aarch64,
with `extra_float_digits=1`. Its native schema had nullable `age:Int` and
nullable `name:String`, with `records.age` bound as `bigint` and `records.name`
as `text`. The JSON shape control keeps `name:String|null` and binds it to
`json_records.name` as native JSON; the required-column control uses
`name:String`. JSON/JSONB `Any` inputs belong to the separate parity fixture.
The temporary shape generator and emitted SQL are
outside the repository; the table below gives the exact HFF queries.

The durable
[`regex_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/regex_integration_test.go)
was copied unchanged into an archive of the baseline before both revisions
were tested: 16,250 PostgreSQL/local
comparisons across 25 patterns and 13 contexts, with 50 inputs per
pattern/context across native String, JSON Any, and JSONB Any bindings, plus
450 null-pattern comparisons, for 16,700 total. A separate
[`mixed_predicates_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/mixed_predicates_integration_test.go)
fixture checks 22 sources against 8 records (176 results). Parity inputs include
null and non-string JSON values, newlines, Unicode, malformed syntax, and long
strings. Unit tests separately exercise unsupported well-formed constructs,
embedded NUL, and invalid UTF-8. The tests in
[`regex_where_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/regex_where_test.go)
cover direct eligibility, argument removal, validation order, value-context
fallback, and source-located errors. Private
[`regex_where_internal_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/regex_where_internal_test.go)
also verifies that custom declarations cannot enter direct lowering.

Run the focused checks with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^(TestNativeLiteralRegexFiltersAvoidProjections|TestDirectRegexKeepsValidationAndValueContexts)$' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^(TestPostgreSQLLiteralRegexParity|TestPostgreSQLMixedPredicatesMatchLocal)$' -count=1
```

## Results

The table reports generated `WHERE` bytes, `OFFSET 0` barriers, and bound
arguments. Representative controls keep their previous lowering.

| Workload | Filter | Baseline bytes / barriers / args | Candidate bytes / barriers / args |
|:--|:--|--:|--:|
| Positive operator | `name~"^A"` | 385 / 3 / 2 | 76 / 0 / 1 |
| Negative operator | `name!~"^A"` | 391 / 3 / 2 | 77 / 0 / 1 |
| Built-in method filter | `name:matches("^A")` | 341 / 3 / 2 | 76 / 0 / 1 |
| Outer negation | `!name~"^A"` | 475 / 4 / 2 | 82 / 0 / 1 |
| Composed native predicates | `age>18 name~"^A"` | 458 / 3 / 3 | 147 / 0 / 2 |
| Invalid pattern | `name!~"["` | 346 / 3 / 1 | 5 / 0 / 0 |
| Null pattern | `name:matches(null)` | 302 / 3 / 0 | 5 / 0 / 0 |
| Mixed arithmetic control | `age+1>10 name~"^A" age+2<30` | 1,512 / 11 / 6 | 1,112 / 7 / 5 |
| Guarded method control | `name!:matches("^A")` | 458 / 4 / 2 | 458 / 4 / 2 |
| Value-context control | `name:matches("^A"):type="null"` | 513 / 4 / 3 | 513 / 4 / 3 |
| JSON receiver control | `name~"^A"` | 549 / 4 / 2 | 549 / 4 / 2 |

Plain `EXPLAIN (COSTS OFF)` on an empty temporary table showed the simple
positive, negative, method, required-column, and combined predicates going
from 3 `Result` / 2 `Nested Loop` nodes to none; outer negation went from 4 / 3
to none. The mixed-arithmetic filter retained 7 `Result` / 6 `Nested Loop`
nodes, down from 11 / 10. Control plans were unchanged. These are plan-shape
observations, not execution measurements.

An optional index diagnostic used an empty table and `pg_trgm` extension:

```sql
CREATE TEMP TABLE records(age bigint, name text);
CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX records_name_trgm ON records USING gin
  ((name COLLATE "C") gin_trgm_ops);
SET enable_seqscan=off;
PREPARE regex_filter(text) AS
  SELECT 1 FROM records
  WHERE name IS NOT NULL AND name COLLATE "C" ~ ($1::text);
EXPLAIN (COSTS OFF) EXECUTE regex_filter(E'(?:\\A\\u0041)');
```

With the actual generated baseline query, the prior `regexp_like()` form could
not use the index; the direct `~` form produced a Bitmap Index Scan with the
regex as an index condition. This demonstrates index eligibility only for
that fixture and matching collation. It does not predict the planner's choice
on populated data or establish an execution-speed gain. No timing or memory
measurement was made.

## Correctness and tradeoffs

The optimization only changes filter lowering for a direct native String
column and a literal pattern. It does not change the accepted RE2 subset,
invalid-pattern behavior, source spans, UTF-8/NUL checks, or parameter order.
The original pattern is checked before translation and only the translated
pattern is bound. In a value context, built-in `matches()` still produces its
nullable result through the general path. JSON, computed, guarded, and dynamic
receivers also retain their existing lowering.

## Decision and limits

Use the direct PostgreSQL `~` and `!~` operators for eligible filter
predicates, with C collation and explicit receiver-null handling. The
index-eligibility result depends on PostgreSQL version, `pg_trgm`, index
definition, and collation; it is not a plan or speed guarantee. PostgreSQL's
[pattern-matching documentation](https://www.postgresql.org/docs/18/functions-matching.html)
states that `regexp_like()` without flags is equivalent to `~`; the generic
method path uses `regexp_like(..., 'cs')` to retain those case and newline
semantics where nullable value results are required.
