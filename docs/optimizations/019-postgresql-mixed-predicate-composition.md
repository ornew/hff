# 019. Compose direct and projected predicates in mixed filters

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

When a boolean filter combines native scalar predicates with expressions that
need general SQL lowering, an all-or-nothing path can place every leaf behind
projection barriers. The compositional path keeps eligible native predicates
in the outer `WHERE` and evaluates generic sibling predicates in a shared
scalar subquery. This lets generic siblings reuse one projected JSON parse,
JSONB metadata restoration, or exact Float kernel instead of constructing a
separate scope for each leaf.

For example, `age>18 raw:fromjson.price>10 raw:fromjson.active` retains the
native `age` comparison outside and evaluates the two `fromjson()` predicates
in a common scope. The query still performs one whole-document validation.
The implementation preserves source-order argument binding. Before splitting
generic scopes, it checks whether their paths repeat or share an ancestor. A
match falls back to general lowering for the whole filter. This conservative
check also covers cheap native columns and can miss safe composition
opportunities.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Mixed filter with eligible native predicates and independent generic siblings | Applied | Direct leaves remain in the outer predicate; generic siblings share one scalar subquery. |
| Generic siblings sharing a parsed JSON or restored JSONB value | Applied | The shared scope retains one parse/validation or metadata-restoration kernel. |
| Planned generic scopes with a shared schema path or ancestor path | Not applied | The conservative overlap check falls back for the whole filter, even when the repeated input is a cheap native column. |
| Callback bodies, captures, guards, and other expression scopes | Partial | Existing binding, presence, and callback rules remain in force; these values are not moved across scopes. |

## Reproduction

The baseline is commit [17b406b](https://github.com/ornew/hff/commit/17b406b).
The candidate comparisons use these filters:

```hff
age>18 name~"^A"
age>18 raw:fromjson.price>10 raw:fromjson.active
age>18 (name~"A"|factor+1.0>2.0)
```

The example schema has nullable `age:Int`, `name:String`, `factor:Float`, and
`raw:String` fields, each bound directly to a same-named column in `records`.
The JSON filter uses `fromjson()` on `raw`; the Float filter uses `factor`.
The reported values are generated `WHERE` character counts and `OFFSET 0`
projection-barrier counts. They describe SQL shape only; no execution timing
was collected. The candidate keeps the same validation requirement for
`fromjson()` and preserves parameter order while combining generic siblings.

The focused Build tests are:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run 'TestMixedPredicates(ExposeDirectLeavesAndPreserveGenericSharing|RetainOneScopeForOverlappingInputs)$' -count=1
```

The focused local/PostgreSQL parity tests are:

```sh
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run 'TestPostgreSQL(MixedPredicatesMatchLocal|NullableAnyStringsMatchLocal|RelationAliasCaptureParity)$' -count=1
```

These and the full unit suite, `go vet ./...`, and
`go vet -tags=integration ./backends/sql` passed. Independent review found no
material issue.

## Results

| HFF filter | Baseline characters / barriers | Candidate characters / barriers |
|:--|--:|--:|
| `age>18 name~"^A"` | 676 / 6 | 458 / 3 |
| `age>18 raw:fromjson.price>10 raw:fromjson.active` | 33,081 / 79 | 32,854 / 76 |
| `age>18 (name~"A"|factor+1.0>2.0)` | 6,171 / 21 | 5,931 / 18 |

The JSON case uses one validation in both forms. Barrier counts and SQL length
do not predict PostgreSQL plan choice or execution time. No speedup is claimed.

For `age>18 name~"^A"`, plain `EXPLAIN` was inspected on an empty temporary
table defined as `age bigint, name text`, with a B-tree index on `age` and
`enable_seqscan` disabled for diagnostic purposes. The baseline showed a
disabled sequential scan with a six-layer `Result` filter subplan. The
candidate showed a Bitmap Heap Scan and Bitmap Index Scan for
`age IS NOT NULL AND age>18`, with the regex retained as a three-layer residual
subplan. To reproduce, create the table and index in a transaction, set
`enable_seqscan` locally, then run `EXPLAIN` on the query using the generated
predicate and its arguments; finish with `ROLLBACK`. This confirms index
visibility for that predicate in that fixture only. It does not show that
PostgreSQL's default planner selects the index or that execution is faster; no
`EXPLAIN ANALYZE` or timing was collected.

## Correctness and tradeoffs

The direct path applies only to eligible predicate leaves; other expressions
retain their ordinary lowering. Boolean composition does not authorize
reordering callback work, changing null/no-value behavior, or moving
expressions across binding scopes. Generic siblings share parse and metadata
work in one scope. If planned generic scopes share a schema path or an ancestor
path, the whole filter retains general lowering. This conservative check also
covers cheap native columns and can miss some safe composition opportunities.

## Decision and limits

Use direct predicates compositionally when eligible leaves can remain outside
a shared generic scope. The three filters above are shape examples, not a
complete eligibility matrix. The fixture-specific `EXPLAIN` observation does
not establish default-planner index use, and SQL size or barrier counts do not
establish execution speed.
