# 027. Reuse bounded equality in PostgreSQL membership

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

PostgreSQL JSON membership helpers could normalize bounded dynamic values into
recursive JSON documents before comparing them. For schema-bounded List,
Map, and Struct alternatives, the translator can instead use the existing
schema-guided recursive equality after the existing schema representability
checks and JSONB metadata requirements. This removes whole-value normalization
and JSON aggregate construction while retaining ordinal list comparison,
actual object keys, nested-null and missing-key behavior, callback scopes, and
ordering.

The optimization does not expand supported stored types or tagged unions.
`Any` at the root keeps the generic path, and nested `Any` remains generic at
that leaf. JSONB numeric metadata and existing rejection behavior remain in
force. In particular, a JSONB/non-numeric `comparisonDocumentAgainst` raw-number
bypass declines this specialization. Representation validation failures fall
back only for typed backend errors; cancellation and unexpected errors
propagate.

## Applicability

| Membership shape | Status | Reason |
|:--|:--|:--|
| Bounded structural candidates | Applied | Reuses typed equality after schema checks. |
| Bounded equality inside a callback | Applied | Preserves callback scope and removes document construction. |
| `Any` root | Not applied | Generic equality remains at the root. |
| Bounded container with an `Any` leaf | Partial | Generic equality remains at that unconstrained leaf. |
| JSONB raw-number comparison against a nonnumeric candidate | Not applied | Retains the existing bypass without introducing null matches. |
| JSONB without required numeric metadata | Not applied | Existing checks and rejections remain. |
| Simple `String\|List<Int>` control | Not applied | It already avoided recursive normalization. |

## Reproduction

The baseline is commit
[e0e5d77](https://github.com/ornew/hff/commit/e0e5d77d25e3ba77e9d408811e1e81fafc0d778d).
Use Go 1.27.1 and PostgreSQL 18.6 with `extra_float_digits=1`. The durable
fixtures are the [schema membership unit tests][unit] and
[PostgreSQL/local integration tests][integration].

[unit]: https://github.com/ornew/hff/blob/main/backends/sql/schema_membership_test.go
[integration]: https://github.com/ornew/hff/blob/main/backends/sql/schema_membership_integration_test.go

The SQL-shape profiles use a `records` table with JSON columns `value`,
`needle`, nullable `items`, and `groups`. The `value` and `needle` fields use
the same compiled type in each profile; `items` is a nullable list of that
type, and `groups` is a list of lists of that type. The measured profiles and
queries are:

| Profile | HFF value type | Query |
|:--|:--|:--|
| `list_in` | `String\|List<Int>\|List<Float>` | `value:in(needle)` |
| `list_contains` | `String\|List<Int>\|List<Float>` | `items:contains(needle)` |
| `list_unique` | `String\|List<Int>\|List<Float>` | `items:unique` |
| `struct_contains` | `String\|Struct{i:Int,f?:Float}\|Struct{i:Int,label?:String}` | `items:contains(needle)` |
| `map_struct_unique` | `String\|Map<Int>\|Struct{fixed:Float}` | `items:unique` |
| `nested_capture` | `String\|List<Int>\|List<Float>` | `groups:any(xs=>xs:contains(needle))` |
| `any_control` | `Any` | `value:in(needle)` |
| `simple_control` | `String\|List<Int>` | `items:contains(needle)` |

Here `?` marks an optional field, not a nullable field. The shape profiles bind
these fields as native JSON. The parity fixtures also exercise JSONB with
numeric type metadata where required, JSON null and SQL NULL, and both sides
of supported and rejected representation boundaries. Plain `EXPLAIN` uses
an empty temporary table with those JSON columns; it is plan-shape inspection
only and does not use `ANALYZE`.

## Results

Each `WHERE` size is bytes. Counts are `OFFSET 0` barriers, recursive CTEs,
and JSON aggregate calls (`json_agg` plus `json_object_agg`), respectively.

| Profile | Baseline bytes / barriers / CTEs / aggregates | Candidate bytes / barriers / CTEs / aggregates |
|:--|--:|--:|
| `list_in` | 8,869 / 16 / 1 / 2 | 5,337 / 18 / 0 / 0 |
| `list_contains` | 8,984 / 16 / 1 / 2 | 5,453 / 18 / 0 / 0 |
| `list_unique` | 8,932 / 14 / 1 / 2 | 5,416 / 16 / 0 / 0 |
| `struct_contains` | 9,936 / 16 / 1 / 2 | 4,188 / 8 / 0 / 0 |
| `map_struct_unique` | 9,486 / 14 / 1 / 2 | 6,040 / 16 / 0 / 0 |
| `nested_capture` | 9,544 / 20 / 1 / 2 | 5,986 / 22 / 0 / 0 |
| `any_control` | 4,972 / 16 / 1 / 0 | 4,972 / 16 / 1 / 0 |
| `simple_control` | 2,250 / 6 / 0 / 0 | 2,250 / 6 / 0 / 0 |

The barrier count increases in several optimized profiles; the change removes
recursive document construction rather than reducing every SQL-shape count.
For the first six profiles, plain `EXPLAIN` plan lines / Nested Loop nodes /
Recursive Union nodes changed, respectively, from 65/16/2 to 45/15/0,
68/16/2 to 48/15/0, 66/15/2 to 46/14/0, 68/16/2 to 28/6/0, 66/15/2 to
46/14/0, and 80/20/2 to 60/19/0. The two controls remained 59/16/2 and
24/5/0. These are fixture-specific plan observations, not timing or speed
claims.

## Correctness and tradeoffs

The focused PostgreSQL/local suite passed 3,888 comparisons on both the pinned
baseline and candidate, plus 12 native-JSON Float-failure checks and 24
asymmetric JSON/JSONB raw-number checks. Full unit tests, `go vet ./...`,
integration-tagged SQL vet, affected SQL race tests, and callback-scope tests
passed. Existing schema-pair, dynamic-contains, no-numeric-metadata,
metadata-alias, guard, JSON parity, Bytes, Duration, and native-Float controls
also passed. These results establish semantic parity, not execution speed.

The direct path still uses schema-guided recursive equality for reachable
containers. It does not remove upstream normalization of an entire receiver
before a membership helper; that is a separate optimization question. The
number of projection barriers rises in some profiles, so SQL size and plan
shape should be read together and not treated as runtime measurements.

## Decision and limits

Reuse bounded structural equality for membership only after existing
representability checks pass. Keep generic equality for `Any`, preserve
metadata and cancellation behavior, and retain the prior fallback for typed
backend errors. Do not infer broader union support, remove whole-receiver
normalization, or claim execution gains from these SQL and plan observations.
