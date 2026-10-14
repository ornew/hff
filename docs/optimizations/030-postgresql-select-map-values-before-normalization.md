# 030. Select JSON Map values before normalization

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

For a schema-known JSON object, normalizing the complete object before a
`get(key)` lookup can process every value even though the query uses one
entry. A Map has one value schema at every key, so the translator can validate
the applicable Map schema, select the raw key candidate, and normalize only
that value. A literal key has a statically known result schema and needs only
that selected schema checked; unrelated Struct fields do not impose
requirements on the selected value.

When the selected value is one known primitive JSON category, a category check
followed by decoding can avoid the JSON comparison-document round-trip. A
selected union or nested container still needs normalization, but only after
selection. The translator projects the candidate once within its existing
scope when normalization can inspect it repeatedly.

Dynamic Map-key lookup checks the complete applicable Map alternatives before
merging their value schemas. This preserves JSONB representation and metadata
requirements and avoids letting a merged type hide an unsupported union. The
dynamic parent object check still precedes key evaluation; missing keys, present JSON
null, SQL null, ordinary null, no-value propagation, and callback binding
identity retain their existing behavior.

A Map or Struct SQL carrier, under its declared storage contract, already
normalizes receiver JSON null to SQL null.
Its `get()` path can omit the redundant object-category check while preserving
receiver-null short-circuiting and failed-guard presence. Dynamic receivers
still need that check, and full object normalization for `values()`/`items()`
retains its existing category mask.

## Applicability

| Access shape | Status | Reason |
|:--|:--|:--|
| Dynamic `get(key)` on schema-known Map alternatives | Applied | Validate the full applicable Map schema, then select the candidate before decoding or normalizing it. |
| Literal-key `get("field")` with one primitive selected type | Applied | Validate the selected schema and category-gate a direct decode; unused fields need not be recoverable. |
| Receiver already has a Map or Struct carrier | Applied | Existing null normalization and presence make another receiver-category check redundant. |
| Selected union or nested Map value | Partial | Normalize only the selected candidate to recover its nested representation. |
| Dynamic key over heterogeneous Struct alternatives | Not applied | Per-key alternatives need Struct dispatch; retain whole-object normalization. |
| `values()` or `items()` | Not applied | These operations observe multiple values or key/value pairs and retain object normalization. |
| Dot and bracket controls | Not applicable | Their existing lowering is unchanged by this work. |

Direct primitive decoding covers schema-known Int, Float, String, Bool, Bytes,
Duration, and Timestamp values, including nullable values. Wrong-category
storage still returns ordinary null. Static literal lookup validates only the
selected value schema, while dynamic Map lookup validates the original
applicable Map schemas before merging. These Build-time checks establish
schema recoverability; consistency between stored rows and caller-provided
metadata remains the caller's responsibility.

## Reproduction

The SQL-shape baseline is commit
[db5518c](https://github.com/ornew/hff/commit/db5518c96d672de61af21027c061cd4583ac4e84).
The temporary shape fixture used Go 1.27.1, PostgreSQL 18.6, and
`extra_float_digits=1`. The schema used `data` as a union of String, Null, and
one object alternative; `key` was String and `groups` was a list of the same
`data` type. `data` and `groups` were native JSON columns and `key` was a
native text column. Object alternatives varied by case:
`Map<Nullable<Int>>`, `Map<Nullable<Float>>`, `Map<String>`, `Map<Any>`,
`Map<Int|Float>`, nested `Map<Map<String>>`, or a Struct. Filters included:

```hff
data:get(key)>10
data:get(key)>10.0
data:get(key):exists
data:get(key):size>0
data:get("a")>10.0
data:get("a")="auth"
groups:any(g=>g:get(key)>10.0)
data.a>10.0
data["a"]>10.0
```

The two known-receiver cases used nullable Map or Struct types without a
String alternative; their column bindings and filters were otherwise the same.

The durable local and PostgreSQL fixtures are the
[unit tests](https://github.com/ornew/hff/blob/main/backends/sql/selected_map_value_test.go)
and [integration tests](https://github.com/ornew/hff/blob/main/backends/sql/selected_map_value_integration_test.go).
SQL-shape measurements came from a temporary local generator whose source and
raw SQL/plans are outside the repository. The
table reports generated `WHERE` bytes, not characters, and `OFFSET 0` barrier
counts. Plain `EXPLAIN (COSTS OFF)` was inspected; no timing benchmark or
`EXPLAIN ANALYZE` was run.

Run the durable correctness fixtures with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestSelected' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLSelectedMap' -count=1
```

## Results

| Workload | Baseline bytes / barriers | Candidate bytes / barriers | `json_each()` occurrences, before → after |
|:--|--:|--:|--:|
| Dynamic Float Map `get(key)>10.0` | 2,797 / 7 | 1,212 / 7 | 1 → 0 |
| Dynamic Int Map `get(key)>10` | 1,430 / 7 | 1,018 / 7 | 1 → 0 |
| Dynamic String Map `get(key)="auth"` | 1,395 / 7 | 1,029 / 7 | 1 → 0 |
| Dynamic Any Map `get(key):exists` | 1,180 / 7 | 912 / 7 | 1 → 0 |
| Dynamic numeric-union Map `get(key):exists` | 2,764 / 7 | 2,424 / 7 | 1 → 0 |
| Nested Map `get(key):size>0` | 1,841 / 8 | 1,559 / 8 | 2 → 1 |
| Static Float `get("a")>10.0` | 3,635 / 5 | 1,139 / 6 | 0 → 0 |
| Static String `get("a")="auth"` | 1,254 / 5 | 956 / 6 | 0 → 0 |
| Callback Float `get(key)>10.0` | 3,312 / 11 | 1,724 / 11 | 1 → 0 |
| Dynamic Struct control | 3,023 / 7 | 3,023 / 7 | 1 → 1 |
| Dot-field control | 775 / 5 | 775 / 5 | 0 → 0 |
| Bracket-field control | 922 / 5 | 922 / 5 | 0 → 0 |
| Known nullable Float Map `get(key)>10.0` | 2,797 / 7 | 1,070 / 7 | 1 → 0 |
| Known nullable Struct Float `get("a")>10.0` | 3,635 / 5 | 997 / 6 | 0 → 0 |

The static primitive lookups became shorter while gaining one projection
barrier for the selected expression. The Map, nested Map, and callback cases
reduced bytes and parent traversal; their barrier counts were unchanged. Struct, dot, and bracket controls were
unchanged. These SQL-shape results do not establish runtime speed.

Plain `EXPLAIN` removed the parent `Aggregate` and `Function Scan` from the
dynamic scalar and union Map cases. The callback case retained its list scan
but removed the Map scan. The nested-Map case retained the selected Map's
normalization and `size()` work, reducing three function scans to two. Static
primitive lookup added a `Result` projection, matching its extra barrier.
These observations concern the fixture plans, not measured execution cost.

## Correctness and tradeoffs

The baseline and candidate each passed 30,240 PostgreSQL/local result matches,
1,024 known-object null/guard presence matches, 168 typed SQL-null carrier
observations, and six JSONB Float-spelling cases. The
matrix included primitive Map values, nested Maps, alternative whole-Map
schemas, nested Struct values, literal
and dynamic keys, empty/slash/tilde/Unicode keys, null and wrong-family keys,
missing keys, nullable and inapplicable receivers, guards, callback captures
and shadowed keys, and nested value observations. It also checked static dot
and bracket controls.
Known nullable Map/Struct fixtures covered receiver SQL/JSON null, missing and
null values, guarded keys, and failed object guards, including negated predicates.

## Decision and limits

For schema-known Map access, validate applicable Map schemas before merging,
select one raw candidate, and normalize only that selected value when its
representation requires it. A single known primitive can be decoded directly
after a category check. Keep whole-object normalization for heterogeneous
Struct dispatch and multi-value enumeration. Reductions in generated SQL are
not runtime-speed evidence.
