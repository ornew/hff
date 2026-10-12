# 010. Restore selected metadata subtrees in structured JSONB bindings

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

With JSONB type metadata attached to a document inside a flattened struct or
relation item, a fixed nested field read previously restored the complete
bound document before selecting the field. The candidate recognizes literal
field chains that cross those physical bindings and extracts the selected
JSONB subtree before restoring its numeric type and value. It carries parent
presence and null state through the projection and keeps metadata pointers
relative to the original document. It adds no binding API and does not validate
stored JSON or metadata; their consistency remains the caller's responsibility.

Direct metadata-backed `BindJSON` already supported selected-subtree
restoration. This change extends that lowering to eligible flattened and
relation-item paths. Whole-document observations and chains whose root or
access depends on a guard, computed value, or dynamic index retain the general
restoration path. The most-specific physical binding remains authoritative.

## Applicability

| Binding / access | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Direct metadata-backed `BindJSON`, fixed nested field | Not applicable (control) | Selected-subtree restoration already existed here; this path was measured as a control. |
| `BindJSON` nested in a flattened `BindStruct`, fixed field chain | Applied | Extracts the nested JSONB subtree while preserving the flattened parent's explicit state. |
| JSONB in a relation item's physical binding, fixed field chain | Applied | Extracts the selected subtree within the relation item's projection scope. |
| Whole-document observation | Not applied | The full document is required by the query. |
| Guarded, computed, or dynamic-index access chain | Not applied | Resolution retains the general restoration path. |
| Native JSON | Not applicable | This candidate concerns metadata restoration and leaves the native JSON lowering unchanged. |
| JSONB without a metadata column | Not applicable | This candidate concerns metadata restoration and does not change this storage mode. |
| Native composite attributes with separate metadata | Not applicable | No per-attribute metadata binding API was added. |

The operation preserves the existing parent missing/null/no-value handling,
callback-scope isolation, and caller-owned version-1 JSON Pointer metadata
contract. It does not inspect or verify stored data during `Build`.

## Reproduction

The baseline is [3ab3913](https://github.com/ornew/hff/commit/3ab391372d753b6bf41a1b482d96edcffac8c3b7). The candidate was measured as source snapshots:

| Candidate source file | SHA-256 |
|:--|:--|
| `backends/sql/structures.go` | `4bf864d4f4d882ae1b0fac0e4a9f3599bc381720215003ca8764e1b48c7b8049` |
| `backends/sql/json_struct_metadata.go` | `e5b3e450bb474287d7e6bb21c30ad0572f30fdaf86cf471aceebf749a6a99f27` |
| `backends/sql/structured_subtree_test.go` | `a31d5c623d49322c6bbfaa189d0f616c010cd84733a2855e510faf62872a2cfb` |
| `backends/sql/structured_subtree_integration_test.go` | `32eadfa36f09085a8bc830bab32aea2dc21522e63e20121b9fb3760a94a8e733` |

The placement benchmark harness was unchanged between baseline and candidate:

| Harness file | SHA-256 |
|:--|:--|
| [`metadata_placement_bench_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/metadata_placement_bench_test.go) | `96745be9771414115cc94aedb2dbd3bd084782f6d124a86cd6ebd081aaa1e2bc` |
| [`metadata_placement_execution_bench_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/metadata_placement_execution_bench_test.go) | `c276f52f5f9ee85540c23330c6e8a859a04cf460c8c6b1874646b3cb901d27ca` |

The six Build cases crossed Direct, Flattened, and Relation placements with a
whole-document `:type="object"` query and a nested `.nested.price>10.5` query.
Database execution used 100- and 1,000-row fixtures. The relation fixture had
one child row per parent and an index on `parent_id`. Each row contained a
Float `price`, five Float list items, and unused nested payload fields; null
documents and negative zero were included. The nested query was rooted at
`doc`, `profile.doc`, or the relation callback's `x.doc`, respectively. Each
execution sample checked exact result IDs against local HFF matching before
measuring the PostgreSQL plan. The correctness matrix included relation items
with both a directly bound JSONB document and a flattened struct item; the
relation benchmark used the flattened struct item.

The host was Darwin 25.2.0, arm64, Apple M3 Max with 64 GiB RAM. Go was 1.27.1
with default `GOMAXPROCS=16`. PostgreSQL was 18.6 in a Debian aarch64 container
using image digest
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`.
There were no explicit CPU, memory, or cpuset limits. JIT was off, parallel
workers were disabled, custom plans were forced, the statement timeout was 60
seconds, and the database directory used tmpfs.

Build command:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataPlacementBuild$' \
  -benchmem -benchtime=200ms -count=3
```

PostgreSQL execution command (use separate plan directories for baseline and
candidate):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-structured-subtree-plans \
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataPlacementExecution$' \
  -benchtime=1x -count=3
```

Raw logs, SQL, plans, exact IDs, and the summary are outside Git in
`/private/tmp/hff-structured-subtree-evidence/` on the measurement host. The
tables use the final three-sample pair after an earlier exploratory pair.

## Results

Build time is median ns/op with the three-sample min–max. `B/op` is cumulative
allocation, not peak or retained memory. `allocs/op`, parameter count, and SQL
bytes are reported separately.

| Placement / query | Baseline ns/op (range) | Candidate ns/op (range) | B/op baseline → candidate | allocs/op baseline → candidate | Args baseline → candidate | SQL bytes baseline → candidate |
|:--|--:|--:|--:|--:|--:|--:|
| Direct / DocumentRoot | 19,194 (19,163–20,798) | 20,448 (20,361–20,678) | 31,879 → 31,880 | 165 → 165 | 1 → 1 | 2,160 → 2,160 |
| Direct / NestedScalar | 26,816 (26,630–26,860) | 28,161 (28,089–28,169) | 67,807 → 67,807 | 267 → 267 | 4 → 4 | 6,871 → 6,871 |
| Flattened / DocumentRoot | 21,290 (21,239–21,395) | 23,091 (22,952–23,408) | 37,153 → 37,329 | 220 → 228 | 1 → 1 | 2,235 → 2,235 |
| Flattened / NestedScalar | 34,208 (33,910–34,527) | 30,840 (30,578–31,122) | 78,180 → 73,233 | 465 → 322 | 3 → 4 | 7,044 → 6,923 |
| Relation / DocumentRoot | 24,948 (24,876–25,019) | 26,765 (26,736–27,016) | 55,531 → 55,532 | 261 → 261 | 1 → 1 | 2,560 → 2,560 |
| Relation / NestedScalar | 34,741 (34,683–35,738) | 34,882 (34,770–35,043) | 119,102 → 115,767 | 328 → 293 | 3 → 4 | 7,373 → 7,252 |

The database timings below are median milliseconds from three single-execution
samples, with min–max ranges. Planning and execution are separate PostgreSQL
measurements; plan-node counts and SQL bytes are baseline → candidate. Matched
rows are the unchanged expected count.

| Placement / rows / query | Planning ms baseline (range) → candidate (range) | Execution ms baseline (range) → candidate (range) | Nodes baseline → candidate | SQL bytes baseline → candidate | Matched rows |
|:--|--:|--:|--:|--:|--:|
| Direct / 100 / DocumentRoot | 0.252 (0.206–0.342) → 0.239 (0.212–0.262) | 12.470 (11.920–12.560) → 12.870 (11.980–13.000) | 24 → 24 | 2,160 → 2,160 | 94 |
| Direct / 100 / NestedScalar | 0.516 (0.493–0.602) → 0.401 (0.386–0.429) | 0.727 (0.668–0.831) → 0.643 (0.589–0.664) | 43 → 43 | 6,871 → 6,871 | 79 |
| Direct / 1,000 / DocumentRoot | 0.202 (0.190–0.230) → 0.248 (0.231–0.262) | 116.200 (115.400–117.100) → 121.100 (121.100–121.600) | 24 → 24 | 2,160 → 2,160 | 941 |
| Direct / 1,000 / NestedScalar | 0.449 (0.440–0.460) → 0.475 (0.419–0.501) | 5.406 (5.381–5.424) → 5.835 (5.681–5.858) | 43 → 43 | 6,871 → 6,871 | 793 |
| Flattened / 100 / DocumentRoot | 0.235 (0.225–0.272) → 0.216 (0.204–0.222) | 11.450 (11.430–11.770) → 12.290 (11.890–12.510) | 24 → 24 | 2,235 → 2,235 | 94 |
| Flattened / 100 / NestedScalar | 0.403 (0.393–0.446) → 0.485 (0.419–0.492) | 12.630 (12.240–12.820) → 0.709 (0.648–0.723) | 47 → 43 | 7,044 → 6,923 | 79 |
| Flattened / 1,000 / DocumentRoot | 0.259 (0.247–0.264) → 0.266 (0.244–0.351) | 116.100 (116.000–118.100) → 125.500 (124.900–126.100) | 24 → 24 | 2,235 → 2,235 | 941 |
| Flattened / 1,000 / NestedScalar | 0.476 (0.462–0.479) → 0.530 (0.487–0.532) | 131.400 (130.700–136.500) → 6.014 (5.410–6.089) | 47 → 43 | 7,044 → 6,923 | 793 |
| Relation / 100 / DocumentRoot | 0.312 (0.311–0.340) → 0.322 (0.266–0.324) | 12.490 (12.150–12.610) → 12.490 (12.470–12.670) | 30 → 30 | 2,560 → 2,560 | 94 |
| Relation / 100 / NestedScalar | 0.507 (0.497–0.697) → 0.531 (0.481–0.551) | 13.200 (12.900–13.450) → 0.737 (0.716–0.738) | 53 → 49 | 7,373 → 7,252 | 79 |
| Relation / 1,000 / DocumentRoot | 0.314 (0.307–0.345) → 0.332 (0.320–0.362) | 122.900 (122.700–124.900) → 124.400 (123.600–125.500) | 30 → 30 | 2,560 → 2,560 | 941 |
| Relation / 1,000 / NestedScalar | 0.683 (0.578–0.690) → 0.600 (0.546–0.665) | 132.700 (131.000–133.100) → 6.694 (6.657–7.221) | 53 → 49 | 7,373 → 7,252 | 793 |

For 1,000-row nested-scalar cases, measured execution medians changed from
131.4 to 6.014 ms for Flattened and 132.7 to 6.694 ms for Relation. Direct
`BindJSON` already had selected-subtree restoration; its nested-scalar
execution control measured 5.406 to 5.835 ms. Whole-document controls and
direct-binding SQL were unchanged. These measurements are specific to these
fixtures, container, and PostgreSQL version; they do not predict application
latency or guarantee a plan for other data.

## Correctness and tradeoffs

The full PostgreSQL integration suite passed in 312.940 seconds. It included
492 comparisons in the new structured-subtree matrix, covering parent state,
key and pointer paths, guards, captures, shadowing, and result-type
observations. Correctness tests cover relation items with both directly bound
JSONB documents and flattened struct items; the relation performance fixture
uses flattened struct items. SQL/local result parity also passed for each
benchmark sample. A binding-configuration test rejects overlapping
ancestor/descendant mappings. The full repository check and
integration-tagged vet passed.

Build timings increased on most controls; the candidate does not establish a
general Build-time improvement. For eligible nested paths, SQL became 121
bytes shorter and the final plan used four fewer nodes in these fixtures, at
the cost of one additional parameter. No retained-memory or peak-memory
measurement was made. Metadata restoration still assumes the caller keeps
stored documents and metadata consistent.

## Decision and limits

Retain selected-subtree restoration for fixed literal field chains within
flattened and relation-item JSONB bindings. Whole-document queries and guarded,
computed, or dynamic-index access continue to use the general path. The
measurement does not add a per-attribute metadata API and does not establish a
benefit for other query shapes, databases, row counts, or document sizes.
