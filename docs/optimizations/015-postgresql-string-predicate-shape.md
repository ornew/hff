# 015. Emit direct predicates for native String operations

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

String predicates and length comparisons on a directly bound native `String`
column were lowered through several `LATERAL` projections with `OFFSET 0`
barriers. The candidate emits `contains()`, `startswith()`, and `endswith()`
directly when the receiver is that column and every argument is a literal
String or null. It also compares `len()` directly with an integer literal.
String predicates retain PostgreSQL's `COLLATE "C"` behavior and explicit
null handling. These forms avoid materializing intermediate HFF values.

For example, the supported source shapes include:

```hff
name:contains("Ali")
name:startswith("Al")
name:endswith("ce")
age>18 name:contains("Ali")
name:len>3
age>18 name:len>3
```

Computed receivers, dynamic or `Any` values, JSON fields, and nonliteral
arguments retain the general lowering.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Direct native String column with literal String/null method arguments | Applied | String method predicates avoid projection barriers. |
| Direct native String `len()` compared with an integer literal | Applied | Length comparison avoids projection barriers. |
| Eligible operation composed with scalar predicates | Applied | The measured conjunction lowers directly. |
| Computed, dynamic/Any, JSON, nonliteral arguments | Not applied | General lowering preserves presence. |

## Reproduction

The SQL-shape comparison uses a directly bound `records.name` text column,
literal String/null method arguments, and integer literals for `len()`
comparisons. Combined queries add a directly bound `records.age` integer
column. Baseline revision is
[88d1bb8](https://github.com/ornew/hff/commit/88d1bb8); the implementation is
commit [a498cde](https://github.com/ornew/hff/commit/a498cde). The code is in
`backends/sql/scalar_where.go` and its focused tests. The reported lengths are
the generated `WHERE` expression lengths; barriers count `OFFSET 0`
projections in those expressions.

Targeted unit tests and local/PostgreSQL result-parity tests passed for the
eligible forms, including nullable values, empty strings, null arguments,
negation, and conjunctions. The direct `len()` comparison was also covered by
targeted unit and local/PostgreSQL parity tests. After the `len()` addition,
`GOCACHE=/private/tmp/hff-go-cache GOTOOLCHAIN=go1.27.1 go test ./...` and
`GOTOOLCHAIN=go1.27.1 go vet ./...` both passed.

## Results

| HFF filter | Prior WHERE length / barriers | Direct WHERE length / barriers |
|:--|--:|--:|
| `name:contains("Ali")` | 449 characters / 3 | 101 characters / 0 |
| `name:startswith("Al")` | 394 characters / 3 | 121 characters / 0 |
| `name:endswith("ce")` | 395 characters / 3 | 122 characters / 0 |
| `age>18 name:contains("Ali")` | 740 characters / 6 | 172 characters / 0 |
| `name:len>3` | 338 characters / 3 | 87 characters / 0 |
| `age>18 name:len>3` | 629 characters / 6 | 158 characters / 0 |

These are SQL-shape observations only. No PostgreSQL plan comparison or
execution timing was collected, so the smaller predicates do not establish a
runtime speedup or index use.

## Correctness and tradeoffs

Eligibility is deliberately narrow: method receivers must resolve directly
to a native String column, and method arguments must be literal String or null
values. The `len()` comparison must use an integer literal. The general path
remains in use for computed receivers, JSON/JSONB, dynamic values, and
expressions as arguments. Targeted parity checks cover nullable inputs, null
arguments, empty strings, negation, length comparisons, and composition with
another eligible scalar predicate. String matching retains the existing
UTF-8 and C-collation semantics.

## Decision and limits

The candidate shortens generated predicates for the listed native-column
shapes while preserving the general lowering elsewhere. This record contains
no runtime or query-plan evidence. Any claim about index selection or database
execution requires a separate measurement on a representative schema and
data set.
