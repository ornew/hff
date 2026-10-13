# 020. Prune schema-impossible numeric branches in JSON comparisons

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Dynamic JSON comparisons select a numeric representation at runtime when the
compiled receiver type permits both Int and Float. Before this change, the SQL
comparison could build both numeric branches even when the compiled receiver
type excluded one subtype. The translator now emits only possible numeric
branches based on that compiled type. For example, a `String|Int` JSONB value
compared with a number needs only the Int branch. A `String|Float` value needs
only the Float branch. An Any value or an Int/Float union still needs every
family its compiled type permits.

The optimization changes generated comparison SQL only. A present JSON value
with a nonnumeric runtime category still produces ordinary null; null or
no-value operands retain the predicate's existing false result. Mixed Int/Float
comparisons continue to use the exact cross-subtype comparison path.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Compiled receiver type permits only Int among numeric families | Applied | The comparison emits the Int conversion and comparison branch only. |
| Compiled receiver type permits only Float among numeric families | Applied | The comparison emits the Float branch only. |
| Receiver permits one numeric subtype and the other operand has another numeric subtype | Applied | Impossible receiver branches are pruned; the required exact mixed-subtype comparison remains. |
| Compiled receiver type permits both Int and Float, or is Any | Not applied | Runtime representation dispatch and any required JSONB metadata recovery remain necessary. |

## Reproduction

The baseline is commit [9d1f24e](https://github.com/ornew/hff/commit/9d1f24e).
The implementation is commit [926b927](https://github.com/ornew/hff/commit/926b927).
The SQL-shape fixture has JSONB columns `records.choice` and
`records.fchoice`, compiled as `String|Int` and `String|Float` respectively.
Neither column uses numeric metadata: each compiled type identifies its only
numeric subtype. A separate parity fixture uses nullable unions and Float
metadata to exercise exact representation recovery, including negative zero.

Representative queries are:

```hff
choice>10
fchoice>1.5
choice>1.5
```

The third query uses the exact mixed Int/Float comparison path. Focused checks
are:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestNumericDispatchOmitsSchemaImpossibleFamilies$' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLSchemaNumericDispatchMatchesLocal$' -count=1
```

The focused comparison matrix covers 30 query forms across eight records,
including nulls, wrong runtime categories, type guards, reversed operands,
signed zero, values near the exact Int/Float boundary, and mixed comparisons.
Three combined native-predicate filters add 24 further cases. Existing
Any-based and mixed numeric comparison cases run in both JSON formats as
controls. The focused PostgreSQL parity tests, full unit suite, `go vet ./...`,
integration-tagged vet, and independent review passed.

## Results

| HFF filter and schema | Baseline `WHERE` characters / barriers | Candidate characters / barriers |
|:--|--:|--:|
| `choice>10`, `choice:String|Int` | 23,949 / 54 | 674 / 4 |
| `fchoice>1.5`, `fchoice:String|Float` | 4,425 / 12 | 3,445 / 10 |

These are generated-SQL shape comparisons, not runtime measurements. No
execution-time, plan, or speed claim is made.

## Correctness and tradeoffs

Branch pruning relies on the compiled receiver type. It does not inspect
stored rows during Build. A valid String or Boolean alternative compared
numerically remains an ordinary-null mismatch; null and no-value comparison
operands preserve their established behavior. When both Int and Float are
possible, exact runtime dispatch remains, and JSONB restoration requirements
are unchanged. The change does not prune dynamic arithmetic branches.

## Decision and limits

Emit only numeric comparison branches allowed by the compiled receiver type.
Keep the general lowering whenever that type permits both subtypes or does not
resolve the representation. The measured SQL reductions do not establish
PostgreSQL plan selection or runtime performance.
