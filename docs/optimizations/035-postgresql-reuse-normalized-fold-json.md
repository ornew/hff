# 035. Reuse normalized JSON across compatible fold states

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL folds keep the accumulator in recursive JSON state. The seed and
each callback result are normalized under their own captured stored
types. When normalization preserves the represented JSON value, repeating
that work for every recursive step adds SQL and JSON expansion without
changing the state.

The translator computes the fold's accumulator carrier from the seed and
callback result types. A proof records the exact immutable JSON expression,
its physical format, and its original stored type. Reuse for a target schema
is allowed only when normalization is proven to preserve every represented
value and the complete target schema passes the existing checks; exact type
keys are not required. The normalized seed establishes the initial case. Each callback result is
validated and normalized under its captured type, and a stability check proves
that it remains normalized under the accumulator carrier. Matching the actual
lowered type to the pre-render inference verifies the assumption used while
lowering the callback. Its scoped predecessor proof is restored or removed
when the callback scope ends.

Compatibility includes cases where normalized values remain unchanged under
a wider carrier, such as Int values in an Int|Float accumulator. Null, Never,
and empty seeds retain their existing fold-state behavior. Map and Struct
widening is checked per key, with ordinary normalization retained when the
proof does not cover the merged shape. The `concat()` input contract remains
unchanged. Each operand is normalized under its existing declared contract;
compatible operands establish a proof for the combined array. Dynamic string
and list branches require separate checks before their wrapper is marked.
Callback-state construction consumes these proofs after its ordinary schema
validation. Guards, extraction, casts, and unrelated derived values do not inherit
a proof. Presence and Float-loss provenance remain separate. A schema that
cannot be proven simply uses the existing normalization path; cancellation
and unexpected errors still propagate.

## Applicability

| Value path | Status | Reason |
|:--|:--|:--|
| Seed and callback results that normalize compatibly to the inferred fold carrier | Applied | The seed and successor checks establish the recursive representation invariant. |
| JSON aliases inside the scoped fold callback | Applied | The proof is tied to the predecessor accumulator expression and restored with its callback scope. |
| Per-key Map and Struct widening | Partial | Reuse applies where every merged position is covered by the proof; uncovered shapes retain normalization. |
| Incompatible output schema, guard, extraction, cast, or unrelated expression | Not applied | These require a different representation proof or the existing fallback. |
| Presence, no-value, Float-loss, and canonical serialization | Not changed | These contracts remain independent of normalized representation reuse. |

## Reproduction

The baseline is commit
[ea1e4cd](https://github.com/ornew/hff/commit/ea1e4cd2918d9d7a849b58144026f0c374996eb9).
The SQL-shape fixture used Go 1.27.1 on Darwin/arm64, PostgreSQL 18.6 on
aarch64, and
`extra_float_digits` 1. It built PostgreSQL filters over JSON `items` and
`needle` bindings. Every `items` field was a nullable List with nullable
elements; `needle` was nullable as well. The element and needle types below
are shown before these wrappers. The listed sources cover left and right folds, list and
Struct accumulators, numeric and object unions, nested folds, plus guarded,
`flatmap()`, `map()`, and simple-size controls. The temporary shape generator
and raw SQL are outside the repository; the query strings are included below
so the workloads can be reconstructed.

The durable PostgreSQL/local fold matrix is in
[`json_normalized_fold_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_normalized_fold_integration_test.go).
It checks 17,216 results on both baseline and implementation across JSON and
metadata-backed JSONB bindings, Float settings 1 and 3, nullable values, captures, shadowed
bindings, nested folds, and serialization/access operations. This includes
13 stored types across 19 contexts, plus Map-to-Struct widening across 22
contexts, with 16 rows per context and four format/Float-setting combinations.
The fixed-point oracle in
[`json_normalization_fixedpoint_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_normalization_fixedpoint_integration_test.go)
checked 3,360 normalization results in the implementation: 84 accepted schema
pairs, 20 JSON values, and two Float settings. Its inputs include
extreme Ints, signed zero, subnormal and underflow values, nulls, and nested
JSON shapes.

Run the focused tests with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^(TestJSONNormalizationFixedPoint|TestNormalized|TestNormalizationOutput)' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQL(NormalizedFoldJSONMatchesLocal|JSONNormalizationFixedPoint)$' -count=1
```

## Results

The table reports generated `WHERE` bytes, occurrences of `OFFSET 0`, and
`json_array_elements` expansion sites. These are SQL-shape counts, not
execution-time or allocation measurements.

| Workload (`items` element / `needle`) | Query | Baseline bytes / barriers / array sites | Candidate bytes / barriers / array sites |
|:--|:--|--:|--:|
| Left-fold list append (`float` / `list[float]`) | `[items:foldl([], (a,x)=>a:concat([x]))]:size>0` | 11,141 / 18 / 8 | 6,101 / 18 / 5 |
| Left-fold identity (`list[float]` / `list[float]`) | `[items:foldl(needle,(a,x)=>a)]:size>0` | 6,878 / 12 / 4 | 3,518 / 12 / 2 |
| Struct replacement (`{i: int, f: float}` / same Struct) | `[items:foldl(needle,(a,x)=>x)]:size>0` | 8,249 / 12 / 1 | 6,110 / 12 / 1 |
| Numeric-union replacement (`int` / `float`) | `items:foldl(needle,(a,x)=>x):json="null"` | 5,825 / 13 / 2 | 4,295 / 13 / 2 |
| Map/Struct overlap (`map[int]` / `{f: float}`) | `items:foldl(needle,(a,x)=>x):json="{}"` | 8,267 / 13 / 2 | 6,082 / 13 / 2 |
| Empty Struct seed (`float` / unused `float`) | `[items:foldl({}, (a,x)=>{"v":x})]:size>0` | 6,323 / 12 / 1 | 4,479 / 12 / 1 |
| Right-fold list append (`float` / `list[float]`) | `[items:foldr([], (x,a)=>[x]:concat(a))]:size>0` | 11,178 / 18 / 8 | 6,138 / 18 / 5 |
| Nested folds (`float` / `list[float]`) | `[items:foldl([], (a,x)=>items:foldl(a,(a,y)=>a:concat([y])))]:size>0` | 15,755 / 25 / 11 | 7,355 / 25 / 6 |
| Fold followed by size (`float` / `list[float]`) | `items:foldl([], (a,x)=>a:concat([x])):size>0` | 9,529 / 17 / 7 | 6,169 / 17 / 5 |
| Guarded fold control (`int` / `float`) | `(items:foldl(needle,(a,x)=>x))!:json="null"` | 5,946 / 14 / 2 | 5,946 / 14 / 2 |
| `flatmap()` control (`float` / unused `float`) | `[items:flatmap(x=>[x])]:size>0` | 5,739 / 10 / 4 | 5,739 / 10 / 4 |
| `map()` control (`float` / unused `float`) | `[items:map(x=>x)]:size>0` | 1,875 / 9 / 1 | 1,875 / 9 / 1 |

The focused fold rows reduce selected JSON-array expansion sites while
leaving projection-barrier counts unchanged. Plain `EXPLAIN (COSTS OFF)` on
an empty `records(items json, needle json)` fixture showed matching reductions
in `Function Scan` nodes: left append 8 to 5, identity 4 to 2, right append
8 to 5, nested folds 11 to 6, and fold followed by size 7 to 5. Object
expansion scans fell from 3 to 2 for Struct replacement and the empty Struct
seed, and from 4 to 3 for the Map/Struct overlap case. Sampled `Result` and
`Nested Loop` counts stayed unchanged; the nested-fold plan retained 25
`Result` and 24 `Nested Loop` nodes. Guarded-fold, `flatmap()`, and `map()` controls kept their SQL shape. No execution timing,
allocation, or retained-memory measurement was made.

## Correctness and tradeoffs

The recursive proof is conditional on both seed and callback result
normalizing compatibly to the inferred accumulator carrier. The implementation
checks the actual lowered callback type against its pre-render inferred type,
so a mismatch uses the ordinary error path instead of reusing a stale proof.
Complete target-schema validation remains before reuse. A schema-representability
error while checking an optional output proof declines the optimization and
keeps the prior normalization path; cancellation and unexpected errors
propagate. SQL NULL versus JSON null, no-value, callback order, captures, and
shadowing keep their existing semantics.

Normalization that is not proven stable still runs. Existing 11,648-result
map parity and 24 PostgreSQL Float decode-failure controls also pass with the
compatible-schema reuse path, as do the existing callback, concat, native
Float, and underflow tests. Unit tests, vet, and affected race checks pass.

`flatmap()` and unrelated
constructed values do not inherit fold proofs. No runtime-speed claim follows
from the SQL-shape reductions.

## Decision and limits

Reuse normalized JSON within fold state only when the seed and each successor
are proven compatible with the inferred accumulator representation. Keep
proofs scoped to their exact SQL value and callback binding. Broader reuse for
other constructed containers, repeated field dispatch, and Float decomposition
requires separate proofs.
