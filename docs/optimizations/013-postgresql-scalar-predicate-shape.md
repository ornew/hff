# 013. Emit eligible scalar predicates directly

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Simple field comparisons were lowered through several `LATERAL` projections
with `OFFSET 0` barriers. That shape can hide a direct column predicate from
PostgreSQL's index planning. For example, the HFF query
`age>18 name="Alice"` produced a 509-character `WHERE` expression with five
barriers. An eligible direct lowering produces a 94-character expression with
no projection barrier, while leaving literals as typed parameters.

The direct path is limited to eligible predicates over directly bound native
scalar columns. It handles literal comparisons and composition with boolean
AND, OR, NOT, null tests, and scalar truth tests. Nullable fields include an
explicit non-null predicate so the result remains a two-valued HFF predicate;
string comparison retains `COLLATE "C"`. Computed expressions, methods,
structured bindings, and other unsupported shapes stay on the general lowering
path.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Direct native scalar columns with supported literal predicates | Applied | Emits column comparisons and boolean composition without intermediate projections. |
| Nullable native scalar columns | Applied | Adds explicit `IS NOT NULL` tests where HFF semantics require them. |
| Computed expressions, method calls, structured fields, and other shapes | Not applied | The general lowering remains responsible for presence and intermediate values. |

## Reproduction

The comparison uses the HFF filter `age>18 name="Alice"` over native `bigint`
and `text` columns. A temporary `records` table has indexes on both columns:

```sql
CREATE TEMP TABLE records (id bigint PRIMARY KEY, age bigint, name text);
CREATE INDEX ON records(age);
CREATE INDEX ON records(name);
```

The baseline generated five `OFFSET 0` barriers; the direct candidate generated
none. The implementation is commit
[3116dfa](https://github.com/ornew/hff/commit/3116dfa). To inspect the generated
plan shape without executing the query, prepare the exact direct candidate
against the same indexed fixture:

```sql
PREPARE hff(bigint, text) AS
SELECT id FROM records
WHERE (("records"."age" > ($1::bigint))
  AND ("records"."name" COLLATE "C" = ($2::text) COLLATE "C"));

EXPLAIN (VERBOSE, COSTS OFF)
EXECUTE hff(18, 'Alice');
```

The direct candidate's plan used a Bitmap Index Scan on `records.age` in the
inspected empty temporary table. The original generated HFF predicate showed a
Seq Scan with a SubPlan. This is a plan-shape observation for that fixture
only; candidate plans on populated, analyzed tables were not measured.

## Results

| Generated predicate | `WHERE` length | `OFFSET 0` barriers | Plan observation |
|:--|--:|--:|:--|
| Prior projection lowering | 509 characters | 5 | Seq Scan with a SubPlan in the inspected fixture. |
| Direct scalar lowering | 94 characters | 0 | Bitmap Index Scan on `records.age` in the inspected fixture. |

No execution timing or `EXPLAIN ANALYZE` measurement was made. The shorter SQL
and its plan do not establish a runtime speedup.

## Correctness and tradeoffs

The direct path must preserve the ordinary lowering's boolean results for
nullable scalar values, including under `NOT`, `AND`, and `OR`. Targeted
PostgreSQL/local parity covers null, boolean, integer, and string fields and
these compositions. Literal values remain bound parameters; string comparisons
retain the HFF byte-lexical collation rule. The eligibility check intentionally
leaves complex expressions to the existing general path.

## Decision and limits

The candidate only changes SQL construction for its eligible scalar subset.
Index use depends on the database, schema, statistics, and data. Validate the
generated predicate and plan for each service query; do not infer execution
performance from SQL length or a plan on an empty table.
