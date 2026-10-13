# 026. Remove proven PostgreSQL arithmetic identities

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

Arithmetic expressions containing identity operands could add projection
barriers even when the result is determined by the other operand. PostgreSQL
Build now removes a closed, statically known identity operand and lowers the
surviving expression once. The rule keeps the original operation's result
subtype, required casts, null/no-value behavior, error behavior, and source
argument order.

The accepted identities are deliberately narrow:

- For Int, `x+0`, `0+x`, `x-0`, `x*1`, `1*x`, and `x/1`. Division by one
  still returns Float, so an Int survivor keeps its required cast.
- For Float, `x*1`, `1*x`, `x/1`, and `x-0.0` when the constant is
  positive zero.

Where the compiled operand types permit it, the zero and one constants may be
Int or Float spellings; the identity must still preserve the operation's
result subtype. Mixed Int/Float arithmetic retains its required
Int-to-Float cast.
Float addition by zero is not simplified because it can change the sign of
zero. Subtraction of negative zero, multiplication by zero, dynamic Any or
Int|Float dispatch, modulo, and arbitrary reassociation keep their existing
lowering.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Statically known Int expression with a proven zero or one | Applied | Only the listed identities are removed. |
| Statically known Float expression with positive one or subtraction of positive zero | Applied | The rewrites preserve signed zero and Float result type. |
| Division by positive one | Applied | The result remains Float, including the required cast from Int. |
| Dynamic Any or Int\|Float expression | Not applied | Runtime family dispatch remains on the general path. |
| Float addition by zero or subtraction of negative zero | Not applied | These operations can change signed-zero results. |
| Multiplication by zero, modulo, or reassociation | Not applied | No identity rewrite is inferred for these expressions. |

## Reproduction

The SQL-shape baseline is commit
[b2baacf](https://github.com/ornew/hff/commit/b2baacf26840474d6c748561112bebc0b1b7b483).
Use the following schema and PostgreSQL bindings for the filters:

| HFF field | HFF type | PostgreSQL binding |
|:--|:--|:--|
| `age` | `Nullable<Int>` | `records.age bigint` |
| `ratio` | `Nullable<Float>` | `records.ratio double precision` |
| `name` | `String` | `records.name text` |
| `value` | `Any` | `records.value json` |
| `items` | `List<Nullable<Float>>` | `records.items json` |

The fixture uses Go 1.27.1 and PostgreSQL 18.6 with
`extra_float_digits=1`. Compile each filter with `capabilities.Standard()`,
build it using the listed bindings, and inspect its plan with plain
`EXPLAIN (COSTS OFF)`:

```hff
age+0>18 name="Alice"
ratio*1.0>18.0
age/1>18.0
ratio*1.0/1.0-0.0>18.0
items:any(x=>x*1.0>18.0)
value*1=0
ratio+0.0>18.0
```

The first five are identity cases; the last two are controls that must retain
their original arithmetic. Coverage spans these binding families and rewrite
boundaries in the
[unit fixture](https://github.com/ornew/hff/blob/main/backends/sql/arithmetic_identity_test.go)
and [PostgreSQL integration fixture](https://github.com/ornew/hff/blob/main/backends/sql/arithmetic_identity_integration_test.go).
The shape generator and raw baseline/candidate outputs are retained outside
the repository at `/private/tmp/hff-identity-shape.go` and
`/private/tmp/hff-identity-*.shape`.

## Results

| Filter | Baseline `WHERE` bytes / barriers | Candidate bytes / barriers |
|:--|--:|--:|
| `age+0>18 name="Alice"` | 608 / 4 | 126 / 0 |
| `ratio*1.0>18.0` | 1,088 / 6 | 78 / 0 |
| `age/1>18.0` | 390 / 3 | 114 / 0 |
| `ratio*1.0/1.0-0.0>18.0` | 2,847 / 14 | 78 / 0 |
| `items:any(x=>x*1.0>18.0)` | 1,952 / 11 | 1,065 / 7 |
| `value*1=0` (dynamic Any control) | 9,141 / 16 | 9,141 / 16 |
| `ratio+0.0>18.0` (signed-zero control) | 1,060 / 6 | 1,060 / 6 |

`WHERE` size is measured in bytes; barriers count `OFFSET 0` occurrences.
Plain `EXPLAIN` plan-line and Nested Loop counts for the first four native
filters changed from 10/3, 15/4, 8/2, and 33/10 to 2/0 each. The callback
filter changed from 33/9 to 20/6. The Any and Float-addition controls stayed
at 40/9 and 15/4. These are plan-shape observations, not execution-time or
speed claims.

## Correctness and tradeoffs

The focused PostgreSQL/local matrix passed 4,140 comparisons on both the
pinned baseline and candidate; 18 additional extension-null carrier cases
passed on the candidate. Coverage includes Float signed zero, null/no-value
propagation, callback scope, type checks, source order, and required
Int-to-Float casts. Full unit tests, `go vet ./...`, integration-tagged vet,
and affected SQL race tests passed. No benchmark or `EXPLAIN ANALYZE` was run.

## Decision and limits

Remove only the proven identities for statically known numeric operands and
validated constants. Evaluate the surviving operand once through its normal
scope. Keep all other arithmetic on the existing path; SQL-size and plan-shape
changes do not establish execution speed.
