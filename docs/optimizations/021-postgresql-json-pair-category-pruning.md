# 021. Prune impossible categories in PostgreSQL JSON pair comparisons

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Dynamic-versus-dynamic JSON comparisons can generate branches for numeric
conversion and recursive container equality even when the operands' compiled
types exclude those categories. The translator intersects the categories
permitted by both compiled operand types before building comparison branches.
It passes only possible Int/Float families to exact numeric comparison and
omits recursive tree equality when no matching array/object category is
possible on both sides. List values map to JSON arrays; Map and Struct values
map to JSON objects.

The intersection runs after stored-value normalization, JSONB metadata
recovery, and unsupported-tagged-union checks. It does not replace those
checks. String and Bool branches are retained only when both operand types can
represent the category. Any contributes its full category set, while the
other operand can still narrow the intersection; Any-versus-Any retains full
dispatch. If a matching container category is reachable on both sides,
recursive equality remains available, including cross-Map/Struct key-set
behavior. This pruning does not remove Float normalization when a Float
comparison branch remains possible; it only avoids branches excluded by the
compiled types.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Scalar unions whose only shared category is String or Bool | Applied | Numeric and recursive-container branches are omitted while the shared scalar comparison remains. |
| Per-operand numeric families are restricted | Partial | Impossible conversions are removed; exact mixed Int/Float comparison remains when the compiled types permit a mixed pair. |
| No matching array/object category is possible on both sides | Applied | Recursive container equality is omitted, including disjoint List versus Map/Struct types. |
| Any versus a restricted type | Partial | Any stays generic on its side, while the restricted operand narrows the shared categories. |
| Any versus Any | Not applied | Full runtime category dispatch remains necessary. |
| Matching containers are possible on both sides | Partial | Recursive structural equality remains for possible container pairs. |
| JSONB values requiring numeric restoration | Applied after existing checks | Existing metadata validation and restoration requirements are unchanged. |

## Reproduction

The baseline is commit [926b927](https://github.com/ornew/hff/commit/926b927).
The shape fixtures are plain JSONB bindings without numeric metadata:

| JSONB binding | Compiled type | Query |
|:--|:--|:--|
| `records.choice` | `String|Bool` | `choice=choice` |
| `records.choice` | `String|Int` | `choice=choice` |
| `records.choice` | `String|Float` | `choice=choice` |
| `records.fchoice`, `records.choice` | `String|Float`, `String|Int` | `fchoice<choice` |

These fixtures measure SQL shape only. Separate parity fixtures use nullable
unions and JSONB Float metadata where needed to cover negative zero. Focused
coverage also includes finite numeric boundaries, asymmetric numeric unions,
disjoint and overlapping List/Map/Struct shapes, arrays, Any, nullable
Map<Any> fields, null and no-value cases, runtime mismatches, NOT, type and
exists observations, guards, SQL NULL versus JSON null, callbacks, and folds.

The focused test sources are `backends/sql/pair_type_dispatch_test.go` and
`backends/sql/pair_type_dispatch_integration_test.go`. The integration test
covers nine profiles and 2,016 query/record comparisons across JSON and
metadata-backed JSONB, including nullable unions, Float metadata, negative
zero, generic Any controls, narrowed `List<Int|String>` callbacks and folds,
captured Any Float values, values beyond 2^53, and values at the int64 limits.
Representative SQL shape cases are:

```hff
choice=choice
fchoice<choice
```

The focused checks used Go 1.27.1:

```sh
GOTOOLCHAIN=go1.27.1 go test ./...
GOTOOLCHAIN=go1.27.1 go vet ./...
GOTOOLCHAIN=go1.27.1 go vet -tags=integration ./backends/sql
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLPairTypeDispatchMatchesLocal$' -count=1
```

The measurements compare generated `WHERE` text length and `OFFSET 0`
projection barriers. They do not measure execution time.

## Results

| HFF filter and compiled types | Baseline `WHERE` characters / barriers | Candidate characters / barriers |
|:--|--:|--:|
| `choice=choice`, `String|Bool` | 111,980 / 230 | 1,520 / 6 |
| `choice=choice`, `String|Int` | 112,070 / 230 | 1,733 / 6 |
| `choice=choice`, `String|Float` | 153,699 / 320 | 48,744 / 108 |
| `fchoice<choice`, `String|Float` vs `String|Int` | 76,702 / 165 | 45,972 / 103 |

These are SQL-shape comparisons, not runtime measurements or benchmark
results. No execution-time, plan, or speed claim is made.

## Correctness and tradeoffs

Schema category intersection must not bypass stored-value validation,
metadata restoration, or unsupported-tagged-union rejection. A valid value
whose runtime category cannot participate in the comparison still follows the
existing ordinary-null mismatch behavior; null/no-value predicate results
remain unchanged. Exact mixed Int/Float comparison is retained whenever the
compiled operand types allow such a pair. Recursive equality is omitted only
when no matching container category can occur on both sides. Existing
schema-known dynamic-pair, Bytes-tagged-union, and scalar numeric-dispatch
controls passed, as did the full Go tests, vet, integration-tagged SQL vet,
and independent review.

## Decision and limits

Use compiled operand types to remove comparison branches that cannot be
reached, after all existing normalization and compatibility checks. Keep
runtime dispatch for Any and preserve the general path wherever an allowed
pair still requires it. The observed SQL reductions do not establish
PostgreSQL plan selection or runtime performance.
