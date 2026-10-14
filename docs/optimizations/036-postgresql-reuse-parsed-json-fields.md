# 036. Reuse parsed JSON field projections

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Repeated references to the same literal field of `fromjson()` output could
repeat field extraction and decoding within one PostgreSQL projection scope.
The translator now shares a parsed carrier after the built-in parser has
lowered successfully, keyed by its SQL expression, presence, HFF kind, JSON
format, static and recovered types, and ordering class. For unguarded literal
field chains rooted at that parser, it can also share the selected field's
extraction and decode. Field projections are keyed by the stable parent alias,
field path, presence, format, types, and order class.

The reuse remains within the existing projection scope. Callback and shadowed
binding scopes continue to reset their shared paths. Guards, type guards,
indexing, extension methods, and arbitrary computed roots retain the existing
lowering. Whole-document syntax, Unicode, duplicate-key, and numeric
validation remains unchanged; this optimization does not remove validation.
Float-loss provenance stays separate.

## Applicability

| Value path | Status | Reason |
|:--|:--|:--|
| Repeated unguarded literal fields from the same built-in `fromjson()` result | Applied | Matching parsed carriers and parent field projections share within one scope. |
| Nested literal Field chains rooted at `fromjson()` | Applied | Stable parent aliases and field paths identify reusable projections. |
| Distinct parser inputs | Not applied | Their SQL expressions and validated documents differ. |
| Guarded/type-guarded roots, indexing, extension methods, computed roots | Not applied | These require separate presence, type, or representation proofs. |
| Callback capture/shadow scopes | Not applied across scopes | Existing scope resets prevent a cache entry from leaking between bindings. |
| Full-document validation and Float-loss handling | Not changed | Validation remains complete; Float-loss provenance is independent. |

## Reproduction

The baseline is commit
[08afb53](https://github.com/ornew/hff/commit/08afb5330ec8d52c089e68c284221b54b49a4075).
The SQL-shape fixture used Go 1.27.1 on Darwin/arm64 and PostgreSQL 18.6 on
aarch64, with `extra_float_digits=1`. Its schema had nullable native String
`data`, required native String `other`, and JSON `items` as a list of String.
It bound `data` and `other` to text columns and `items` to a JSON column in
`records`. The temporary shape generator and emitted SQL are outside the
repository; the table below gives the exact HFF queries.

The durable
[`json_parse_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_parse_integration_test.go)
checks 7,644 PostgreSQL/local results for JSON, JSONB, and native nullable text
bindings. The expanded fixture was copied unchanged into an archive of the
baseline before running both revisions. It covers syntax errors, duplicate keys, Unicode, numeric ranges,
full-document validation, guards, captures, shadowing, and nested field
chains. Unit tests
[`json_parsed_cache_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_parsed_cache_test.go)
and
[`json_parsed_cache_public_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/json_parsed_cache_public_test.go)
check carrier identity, presence, format, type, and field-root eligibility.

Run the focused tests with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^(TestParsedValueCacheRetainsRepresentationAndPresence|TestParsedJSONFieldsShareOnlyStableParents)$' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLStrictJSONParsing$' -count=1
```

## Results

The table reports generated `WHERE` bytes, `OFFSET 0` barriers, and
`IS JSON WITH UNIQUE KEYS` validations. The validation count is unchanged in
the eligible rows: the optimization reuses field projections without skipping
whole-document validation.

| Workload | Query | Baseline bytes / barriers / validations | Candidate bytes / barriers / validations |
|:--|:--|--:|--:|
| Repeated field | `data:fromjson.a=1 data:fromjson.a<2` | 9,399 / 20 / 1 | 7,987 / 16 / 1 |
| Nested field | `data:fromjson.a.b=1 data:fromjson.a.b<2` | 9,839 / 24 / 1 | 8,207 / 18 / 1 |
| Repeated type observation | `data:fromjson.a:type=data:fromjson.a:type` | 2,790 / 14 / 1 | 2,437 / 11 / 1 |
| Separate parser inputs | `data:fromjson.a=1 other:fromjson.a=2` | 10,926 / 25 / 2 | 10,926 / 25 / 2 |
| Guarded and unguarded parser uses | `data:fromjson!.a=1 data:fromjson.a=2` | 9,528 / 21 / 1 | 9,395 / 20 / 1 |
| Callback-local reuse | `items:any(data=>data:fromjson.a=1 data:fromjson.a<2)` | 10,078 / 25 / 1 | 8,664 / 21 / 1 |
| Nested shadow control | `items:any(x=>x:fromjson.a=1 items:any(x=>x:fromjson.a=2))` | 12,281 / 35 / 2 | 12,281 / 35 / 2 |
| Captured parser control | `items:any(x=>data:fromjson.a=1 items:any(x=>data:fromjson.a=2))` | 11,959 / 33 / 2 | 11,959 / 33 / 2 |
| Native String control | `other~"^A"` | 386 / 3 / 0 | 386 / 3 / 0 |

Plain `EXPLAIN (COSTS OFF)` on the corresponding `records(data text, other
text, items json)` fixture showed fewer `Result` and `Nested Loop` nodes for
the eligible repeated-field cases. For the simple repeated field, counts fell
from 22 to 18 `Result` and 20 to 16 `Nested Loop` nodes; the nested path fell
from 26 to 20 and 24 to 18. The type-observation case fell from 16 to 13 and
14 to 11. Callback-local reuse fell from 27 to 23 and 25 to 21. `Function
Scan` counts were unchanged, consistent with retaining full JSON validation.
The guarded field in the mixed guarded/unguarded workload retains ordinary
lowering; only its unguarded parser use can share a matching carrier. The
separate-input, nested-shadow, captured-parser, and native String controls
retained their plan counts. No execution timing, allocation, or retained-memory
measurement was made.

## Correctness and tradeoffs

The cache key distinguishes expressions, presence, HFF kind, JSON format,
static/recovered type, ordering class, and literal field path. Full-document
validation still precedes field use. Reuse does not cross guards, indexing,
extensions, arbitrary computed roots, or callback/capture scopes. SQL NULL,
JSON null, and no-value behavior remain distinct.

The measured SQL and plan changes apply to these repeated unguarded literal
field workloads. Validation scans and Function Scan counts remain; no runtime
speed claim follows from the smaller expressions or plans.

## Decision and limits

Share successfully decoded built-in `fromjson()` carriers and unguarded
literal-field projections only when their full cache keys match within the
same projection scope. Keep validation and Float-loss handling intact. Do not
extend this proof to guarded, indexed, extension, or arbitrary computed
expressions without separate representation and scope checks.
