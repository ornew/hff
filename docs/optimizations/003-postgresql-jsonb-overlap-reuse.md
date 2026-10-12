# 003. Reuse restored JSONB parents across overlapping paths

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

Selected-subtree restoration can reconstruct the same JSONB ancestor more
than once when a query refers to both that parent and its descendants. The
candidate reuses an ancestor already restored in the same projection scope,
then extracts the descendant from that restored JSON text. The parent's
restoration has already applied metadata using document-relative JSON Pointers;
the descendant does not perform another metadata lookup. It does not hoist or
reorder expressions. The most-specific physical binding remains authoritative.

The candidate is useful only when the ancestor was already restored before a
descendant is lowered. A scalar-only query has no reusable parent. A query that
uses a child before the parent may not reuse it. Native JSON does not need
numeric metadata restoration.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Direct JSONB metadata binding, parent then descendant in one projection scope | Partial | The fixed unguarded descendant can reuse an already restored parent. A direct nested `BindJSON` can qualify within its own binding boundary. |
| Scalar-only JSONB access | Not applicable | There is no restored ancestor to reuse. |
| Descendant before parent | Not applied | The parent is not yet present in the projection cache. |
| Native JSON | Not applicable | No JSONB numeric metadata restoration is needed. |
| Guarded descendant or callback-item / flattened physical binding | Not applied | Guarded values are not shared; callback-item and flattened physical bindings use separate lowering paths. |
| Separate order-demand scope | Not applied | Order-demand projections retain their own cache scope. |

Only an unguarded prefix already restored in the current projection scope can
be reused. A prefix before a later guard can qualify, but the guarded value
itself is not shared. This is a translation-shape condition, not a database
plan or execution-order guarantee.

## Reproduction

The baseline is [558d5b87](https://github.com/ornew/hff/commit/558d5b87f241e1e394ec9ad0b7cb1f69248888c5).
The candidate source snapshot was measured before its integration commit. Its
SHA-256 inputs are recorded in `/private/tmp/hff-overlap-evidence/final-source-hashes.txt`:

| File | SHA-256 |
|:--|:--|
| `backends/sql/metadata_bench_test.go` | `7d9616454868f0cf1fa08cae76ad36dabd850e428996852074fd84bf76e3ffb7` |
| `backends/sql/json.go` | `2402f4a66e3941697c89050a061a628f514fc73300aa3b29ec61546e2f6a8d5d` |
| `backends/sql/json_metadata_parent.go` | `9a1bafb97b3a4b4e174aae9e523841e289b0958f2072fdc539d5b0a515243c0b` |

The shared benchmark harness and fixtures were applied to both revisions. Build
measurements used Go 1.27.1 with `-benchtime=200ms -count=3`; PostgreSQL
execution measurements used `-benchtime=1x -count=3`. Both used the default
`GOMAXPROCS=16` shown in the benchmark output. The measured comparison selected
four query shapes: scalar access, parent then child, nested parent then
descendants, and children before parent. The harness also contains repeated,
guard, and callback queries, but those were not part of this measured
comparison. The execution matrix covered native JSON and JSONB with version-1
metadata, and 100 and 1,000 rows. Fixtures included nulls, signed-zero floats,
five numeric list entries, and unused nested payload values. Each execution
sample checked exact SQL result IDs against local matching before `EXPLAIN`.

The host was Darwin 25.2.0, arm64, Apple M3 Max with 64 GiB RAM. PostgreSQL was
18.6 on Debian aarch64, using image digest
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722` on a
network-disabled container. Tests used JIT off, zero parallel workers, custom
plans, and a 60-second statement timeout. The run alternated baseline and
candidate A/B/A/B, followed by a final A/B pair. Raw logs, plans, SQL, exact
IDs, and the summary are outside Git in `/private/tmp/hff-overlap-evidence/`.
The container had no explicit memory limit, CPU quota, or CPU set configured.

Re-run Build separately from PostgreSQL execution:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataBuild/(NativeJSON|JSONBMetadata)/(Scalar|RootAndChild|NestedAndChildren|ChildrenThenParent)' \
  -benchtime=200ms -count=3
```

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-overlap-plans \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataExecution/(NativeJSON|JSONBMetadata)/(100|1000)Rows/(Scalar|RootAndChild|NestedAndChildren|ChildrenThenParent)' \
  -benchtime=1x -count=3
```

Use separate plan directories for baseline and candidate because the harness
writes one plan, SQL statement, and exact-ID file per case.

## Results

The focused 1,000-row JSONB-with-metadata cases show both the applicable path
and controls where reuse did not help. Values are three-sample medians; ranges
are min–max where shown. Build `B/op` measures cumulative allocations, not
retained or peak memory.

| Workload | Build µs/op, B/op, allocs/op; SQL bytes/args | Plan nodes; planning ms; execution ms (min–max) |
|:--|:--|:--|
| `NestedAndChildren` | 41.355 → 39.781; 98,026 → 83,765; 572 → 531; 11,949/11 → 8,302/7 | 99 → 63; 1.049 → 0.596; 88.27 (88.10–89.53) → 80.62 (80.34–81.56) |
| `RootAndChild` | 29.720 → 29.354; 77,698 → 71,595; 316 → 310; 8,869/4 → 7,210/3 | 67 → 51; 0.667 → 0.462; 168.9 (168.1–172.1) → 170.5 (169.5–173.2) |
| `ChildrenThenParent` | 34.121 → 34.408; 82,370 → 82,530; 404 → 415; 9,276/7 → 9,276/7 | 71 → 71; 0.722 → 0.664; 82.99 (81.71–90.10) → 83.10 (82.55–83.57) |
| `Scalar` control | 25.887 → 25.918; 67,468 → 67,468; 227 → 227; 6,641/3 → 6,641/3 | 41 → 41; 0.447 → 0.403; 7.136 (7.135–7.141) → 7.043 (6.979–7.151) |

All four execution cases matched 793 of 1,000 rows on both revisions. The
`NestedAndChildren` case is the clear improvement in this fixture. `RootAndChild`
has a smaller query and plan but does not improve execution time. Reversing the
path order does not reuse the parent and shows no improvement. Scalar access is
a control and its SQL and allocation counts are unchanged. Native JSON controls
also have unchanged generated SQL and allocation counts.

## Correctness and tradeoffs

The parity matrix covered escaped JSON Pointer paths, signed zero, null and
missing values, scalar parents, guards, direct nested JSON bindings, and
callback scope. A focused boundary test verified that two independently bound
nested JSON columns restore their own parents and reuse descendants only within
each binding. Reuse stays within an existing projection scope; it does not merge
callback or order-demand caches. The optimization adds a selection condition
and remains dependent on expression order in the translated query.

## Decision and limits

The candidate reduces Build allocations, SQL size, and PostgreSQL execution
time for the nested parent-before-child workload measured here. The other
workloads show that smaller SQL or fewer plan nodes do not imply lower
execution time. These results do not establish a general speedup, retained
memory reduction, or behavior for other schemas, data distributions, database
versions, or service workloads. The measured source snapshot is identified by
the hashes above. Full PostgreSQL integration and project formatting, race,
vet, generation, examples, WASM, and site checks passed.
