# 023. Lower PostgreSQL Float work with the native approximate model

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

After scalar comparisons stopped round-tripping values through JSON, the
PostgreSQL Float path still used a custom underflow decoder and exact
formatting and arithmetic emulation. This step removes those mechanisms in
favor of PostgreSQL's native Float model: `double precision` for ordinary
operations and checked `numeric` fallback for extreme arithmetic magnitudes.

The change preserves Int/Float subtype markers, exact Int operations, exact
mixed Int/Float comparisons, null behavior for invalid operations, and JSONB
metadata requirements where storage lost an observable Float representation.
It does not make Float results or decimal text identical to the Local backend.

## Applicability

| Workload | Status | Reason |
|:--|:--|:--|
| Dynamic scalar Float self-comparison | Applied | Consume Float values through PostgreSQL's native model without canonical JSON round-trips. |
| Dynamic Float/Int ordering | Applied | Use the native Float result model while retaining exact mixed numeric ordering. |
| Float arithmetic, parsing, rounding, and output | Applied | Follow the Float profile and checked extreme-value conversion rules. |
| Int-only operations and exact mixed numeric comparison | Not changed | These retain their existing exact contracts. |
| Structural Float sort keys | Not changed | Some structural sort paths still use exact decomposition. |

## Reproduction

The SQL-shape baseline is commit
[88c545c](https://github.com/ornew/hff/commit/88c545c). The focused pair
fixtures use schema-known JSONB fields whose possible values are `String|Float`
and `String|Float` versus `String|Int`. The Float schema fixes the numeric
subtype in these shape cases; additional parity fixtures exercise native JSON
and metadata-backed JSONB, including signed zero and boundary values.

The implementation is covered by the repository unit and vet suites, focused
PostgreSQL/local Float comparison and serialization tests, native Float
boundary tests, and JSON structural precision regressions. Plain plans were
inspected without `ANALYZE`; no timing or benchmark was collected. The focused
commands are:

```sh
GOTOOLCHAIN=go1.27.1 go test ./...
GOTOOLCHAIN=go1.27.1 go vet ./...
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run 'TestPostgreSQL(NativeFloatModel|NativeFloatAndBytesInMatchesLocal|NativeFloatStructuralPrecision|JSONFloatUnderflow|FloatArithmetic|NumericRounding|PrimitiveStringConversions|StrictJSONParsing|SharedScalarConformance|DynamicJSONPairs|SchemaKnownDynamicPairs)$' -count=1
```

## Results

| Filter | Baseline `WHERE` bytes / barriers | Native-model `WHERE` bytes / barriers |
|:--|--:|--:|
| `fchoice=fchoice`, `String|Float` | 6,570 / 16 | 1,308 / 4 |
| `fchoice<choice`, `String|Float` vs `String|Int` | 4,907 / 18 | 2,257 / 12 |

For the `fchoice=fchoice` plan, the baseline had 34 plan lines, 13 nested-loop
nodes, and no `generate_series` function scan. The candidate had 10 plan lines,
3 nested-loop nodes, and no `generate_series` scan. These are plan-structure
observations, not execution-time or speed claims.

## Correctness and tradeoffs

The focused suites include 3,528 PostgreSQL native-rounding results, the
2,016-case dynamic-pair matrix, 208 physical-spelling checks, 324 canonical
JSON controls, native arithmetic scopes, 23 Float boundary/session cases, and
eight structural precision regressions. Additional coverage includes 4,559
strict JSON parsing cases, 8,262 primitive string-conversion cases, and 87
shared scalar-conformance cases. Int behavior, exact mixed comparisons,
subtype markers, and JSONB representability checks remain covered separately.

Native Float semantics can differ at rounding boundaries and in decimal text.
The PostgreSQL JSON text transport requires `extra_float_digits > 0`; HFF does
not set or inspect this session setting. Applications must satisfy the
backend's Float profile requirements.

## Decision and limits

The PostgreSQL backend uses its declared native Float model for the supported
Float operations. This record makes no runtime-speed claim. Exact Float
decomposition remains in some structural sorting code; the decision does not
claim that every SQL path has eliminated it.
