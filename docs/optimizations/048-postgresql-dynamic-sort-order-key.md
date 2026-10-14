# 048. Reuse the exact numeric ordering key for dynamic JSON sorts

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Dynamic PostgreSQL JSON sorting used a binary Float decomposition to produce
keys that compared Float values exactly with Int values. PostgreSQL already
has a compact numeric ordering key used by numeric extrema and structural
`distinct()`. Dynamic sorting now uses that same key for each decoded Float.

The key projects large integral Floats within the int64 range to exact numeric
integers, handles positive 2^63 as an exact numeric endpoint, and converts
other Float values from PostgreSQL's shortest-precise output to `numeric`.
This preserves comparisons against Int: an integral Float below 2^52 has an
exact integer representation of at most 16 digits, while a nonintegral
Float's round-trip interval cannot cross an integer. Values whose integral
representation can overlap int64 use the exact integer path; the 2^63 endpoint
is handled explicitly. Int values remain exact. Sorting still uses the
original item and its ordinal, so equal numeric values, including signed-zero
ties, retain their input order and representation when recoverable. JSONB
metadata remains required when normalization has erased an observable
Int/Float distinction.

For representable Float values, the ordering and tie behavior are unchanged.
Separately, the PostgreSQL Float Profile rejects an unrepresentable Float key
as null consistently for dynamic `Any` JSON and typed JSON list paths. This
corrects a difference between those paths; it does not make PostgreSQL and
Local agree on raw Float underflow spellings.

## Applicability

| Workload or profile | Status | Reason |
|:--|:--|:--|
| Dynamic JSON `Any` numbers | Applied | Float values use the shared exact numeric ordering key. |
| Dynamic JSON `List<Int\|Float>` | Applied | Int and Float keys compare represented values without rounding an Int through Float. |
| Dynamic JSONB with recoverable numeric subtypes | Applied | Metadata restoration remains in front of the shared key where required. |
| Stable ties and source order | Applied | The original ordinal remains the final order key. |
| Homogeneous schema-known lists and non-JSON backends | Not changed | Their existing sort paths are unaffected. |
| Unrepresentable Float key | Applied correctness boundary | The PostgreSQL Float Profile yields null consistently across dynamic `Any` JSON and typed JSON list paths. |
| Erased JSONB representation without recovery metadata | Not applied | Existing numeric subtype and representation requirements remain authoritative. |

## Reproduction

The baseline is [8234af4](https://github.com/ornew/hff/commit/8234af48836a9d4507a429a958b4d170c21c2319).
The fixture was added with this implementation at
[`hack/observe-sort-key/main.go`](https://github.com/ornew/hff/blob/main/hack/observe-sort-key/main.go).
To reproduce the baseline, preserve a copy of the fixture outside the checkout
before switching revisions, then run the same source on each revision. The
fixture builds three filters for each of `Any` and `List<Int|Float>` using
native JSON and metadata-backed JSONB:

```hff
data:sort
data:sort[0]>0
data:sort:json="[1,2.0,3]"
```

With Go 1.27.1, run the fixture and focused tests:

```sh
go run ./hack/observe-sort-key
go test ./backends/sql -run '^TestDynamicSortUsesCompactNumericOrderKey$' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLSortKeyNumericBoundaries$' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLDynamicSortParity$' -count=1
```

The PostgreSQL boundary matrix compares an exact-rational oracle for `Any` and
`List<Int|Float>` values in JSON and metadata-backed JSONB. Its eight lists
cover values around ±2^52 and ±2^53, int64 endpoints, adjacent fractions,
signed zero, subnormals, and the largest finite Float. The measured fixture
used Go 1.27.1 on Darwin arm64 and PostgreSQL 18.6 on Debian aarch64 with
`extra_float_digits=1`. The Build outputs are retained outside the repository
at `/private/tmp/hff-sort-key-baseline.jsonl` and
`/private/tmp/hff-sort-key-candidate.jsonl`.

## Results

Generated `WHERE` byte counts were measured with `len(q.Where())`; argument
counts were unchanged:

| Binding | Format | Filter | Baseline bytes | Candidate bytes | Args |
|:--|:--|:--|--:|--:|:--:|
| `Any` | JSON | `data:sort` | 3,355 | 2,395 | 0 |
| `Any` | JSON | `data:sort[0]>0` | 7,078 | 6,073 | 2 |
| `Any` | JSON | `data:sort:json="[1,2.0,3]"` | 5,835 | 4,852 | 1 |
| `Any` | JSONB | `data:sort` | 5,059 | 4,079 | 0 |
| `Any` | JSONB | `data:sort[0]>0` | 8,782 | 7,802 | 2 |
| `Any` | JSONB | `data:sort:json="[1,2.0,3]"` | 7,542 | 6,559 | 1 |
| `List<Int\|Float>` | JSON | `data:sort` | 5,335 | 4,364 | 0 |
| `List<Int\|Float>` | JSON | `data:sort[0]>0` | 8,972 | 8,000 | 2 |
| `List<Int\|Float>` | JSON | `data:sort:json="[1,2.0,3]"` | 9,420 | 8,427 | 1 |
| `List<Int\|Float>` | JSONB | `data:sort` | 7,046 | 6,057 | 0 |
| `List<Int\|Float>` | JSONB | `data:sort[0]>0` | 10,683 | 9,694 | 2 |
| `List<Int\|Float>` | JSONB | `data:sort:json="[1,2.0,3]"` | 11,131 | 10,142 | 1 |

The table reports SQL size and argument count. It does not measure execution
time, allocation, or memory. Plan-shape observations are reported separately
below.

## Correctness and tradeoffs

The focused exact-rational PostgreSQL boundary test passed all 32 combinations
of schema, storage format, and list fixture. Dynamic sorting parity passed
2,720 results with the PostgreSQL Float Profile oracle. PostgreSQL rejects a
raw native JSON `-1e-400` Float as unrepresentable, so its sort result is null;
metadata-backed JSONB negative zero remains unchanged. The same oracle applied
to the pinned baseline exposed 12 mismatches between its dynamic `Any` and
typed-list paths. The implementation makes those paths consistent with the
PostgreSQL Float Profile. This correctness repair is distinct from the
ordering-key change, so the SQL-size comparison is not a claim of baseline
equivalence for unrepresentable Float inputs. The original ordinal continues
to break equal-key ties, preserving stable order and signed-zero spelling
when the storage representation retains it.

A plain `EXPLAIN (FORMAT JSON, COSTS OFF)` on the empty fixture counted 24
baseline plan nodes and 19 candidate nodes. Both plans retained the same three
array `Function Scan` nodes. This is plan-shape evidence only; neither plan was
executed for timing.

The compact key is shared with PostgreSQL numeric extrema and structural
`distinct()`. It uses Float text for values whose exact integral value is not
needed to compare against int64, while preserving exact integral Float values
where their magnitude can overlap the Int range. This avoids binary Float
decomposition without rounding Int values through Float.

## Decision and limits

Use the shared numeric ordering key in the dynamic JSON sorting path. Keep the
existing JSON/JSONB representation checks, stable ordinal tie-breaker, and
fallback behavior. The recorded SQL-size reductions do not establish faster
execution or a particular plan for other schemas and workloads.
