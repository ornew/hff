# 028. Prune schema-impossible numeric arithmetic branches

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

Dynamic JSON arithmetic must validate the stored representation and honor the
compiled receiver type before evaluating an operation. The general lowering
previously built both Int and Float arithmetic branches even when that type
excluded one family. The translator now intersects the compiled type with the
stored schema's possible numeric families after the existing representation
and metadata checks, then emits only reachable arithmetic, negation, or
`round()`/`ceil()`/`floor()` branches.

This changes SQL construction, not value semantics. Int overflow, division
results, and required Int-to-Float promotion remain intact. Float work still
follows PostgreSQL's native Float Profile; this change does not claim that its
results match the local backend at underflow boundaries. Null and no-value
behavior, callback scope, and typed SQL-null carriers are preserved. Mixed
Int/Float operations retain the required numeric paths; mixed comparisons
remain exact, while mixed arithmetic follows the PostgreSQL Float Profile.

## Applicability

| Compiled value type and operation | Status | Reason |
|:--|:--|:--|
| `String\|Int\|Null` arithmetic, negation, or numeric rounding | Applied | Only the Int input family is reachable after stored-value checks; operation-specific results such as Int division to Float remain. |
| `String\|Float\|Null` arithmetic, negation, or numeric rounding | Partial | The unreachable Int input branch is removed; Float uses its existing general PostgreSQL kernel. |
| `String\|Int\|Float\|Null` arithmetic or rounding | Partial | Branches excluded by the compiled type or operation are removed; reachable numeric families remain. |
| Unguarded `Any` arithmetic or rounding | Not applied | Both numeric input families may occur, so runtime dispatch remains necessary. |
| Mixed Int/Float operands | Applied | Required promotion and mixed-family behavior remain; only impossible result branches are removed. |
| Direct native scalar and nonnumeric controls | Not applicable | These do not use dynamic JSON numeric dispatch. |

The same narrowing applies inside supported callbacks and when a compiled type
guard restricts a dynamic value. A guard does not bypass stored-value
representability or JSONB metadata checks. Int division still returns Float,
even when its input operands are Int.

## Reproduction

The baseline is commit [46ddd93](https://github.com/ornew/hff/commit/46ddd93).
The shape harness used Go 1.27.1 and PostgreSQL 18.6 with
`extra_float_digits=1`, the PostgreSQL native Float profile. Its engine schema
declared `i` as `String|Int|Null`, `f` as `String|Float|Null`, `both` as their
union, `data` as `Any`, `items` as `List<String|Int|Null>`, `guard` as nullable
Int, and `name` as String. Paths `i`, `f`, `both`, `data`, and `items` were
bound to matching native JSON columns on `records`; `guard` and `name` were
bound to native columns. All builds used the PostgreSQL dialect. Durable
semantic fixtures are
[`numeric_branch_pruning_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/numeric_branch_pruning_test.go)
and
[`numeric_branch_pruning_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/numeric_branch_pruning_integration_test.go).
The generated SQL-shape table came from a separate temporary local harness;
its source and raw SQL/plans are not included in the repository.

Representative filters are:

```hff
i+2>0
f+2.0>0
i/2>0
items:any(x=>x+2>0)
(data:[string|int]+2):type="int"
i+f>0
```

Focused validation included:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestNumericBranch' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLNumericBranchPruning' -count=1
```

The JSON/JSONB parity matrix covered 4,064 local/PostgreSQL comparisons against
both the pinned baseline and implementation, including nullable and
wrong-category values, type guards, callback scopes, mixed numeric values,
signed zero, and Int boundaries. Thirty-six extension observations checked
that SQL null carriers retained their expected types. Numeric rounding,
native Float, underflow, and schema-dispatch controls also passed. No timing or
benchmark was collected.

## Results

| Filter | Baseline `WHERE` bytes / barriers | Candidate bytes / barriers |
|:--|--:|--:|
| `i+2>0` | 5,277 / 14 | 1,065 / 6 |
| `(data:[string\|int]+2):type="int"` | 5,981 / 17 | 1,761 / 9 |
| `items:any(x=>x+2>0)` | 5,797 / 18 | 1,577 / 10 |
| `i/2>0` | 7,211 / 15 | 2,811 / 7 |
| `f+2.0>0` | 5,994 / 16 | 5,454 / 15 |
| `i+f>0` | 6,672 / 18 | 5,922 / 17 |
| `(both+2):type="int"` control | 5,944 / 15 | 5,922 / 15 |
| `(data+2):type="int"` Any receiver with a result type test | 5,944 / 15 | 5,922 / 15 |
| `name="Alice"` control | 55 / 0 | 55 / 0 |

The `String|Float` example removes its unreachable Int input branch but keeps
the general Float kernel. The Int/Float and unguarded Any controls retain both
numeric families. The type-guarded Any example narrows to Int. Int division
still has a Float result. Counts describe generated SQL shape, not runtime
cost.

Plain `EXPLAIN (COSTS OFF)` on generated fixtures showed fewer `Result` and
`Subquery Scan` nodes for representative Int-only arithmetic and callback
filters. The Float-only filter retained its Float subquery structure. These
are fixture-specific plan observations; there was no `EXPLAIN ANALYZE` or
execution-time measurement.

## Correctness and tradeoffs

Pruning uses compiled possible types only after the existing storage
representability and metadata checks. It does not inspect stored rows at
Build time or accept invalid stored values. Any keeps both numeric families;
an Int/Float union keeps both when both are reachable. Mixed arithmetic still
promotes Int as required, and Int division remains Float. Mixed comparisons
retain their exact represented-value behavior. The native Float
Profile, including its backend-specific underflow behavior, is unchanged.

## Decision and limits

Emit only numeric arithmetic and rounding branches permitted by the compiled
type and operation. Keep the general lowering where the value remains
unconstrained or both families are reachable. These SQL-shape reductions do
not establish execution speed or eliminate the general Float kernel.
