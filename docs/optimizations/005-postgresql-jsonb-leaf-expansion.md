# 005. Avoid expanding scalar leaves during JSONB metadata restoration

- **Status**: Measured
- **Date**: 2026-10-13

## Problem and mechanism

Recursive JSONB metadata restoration must visit document nodes to restore
numeric representations and reconstruct the document. Its recursive term also
attempted child expansion for scalar leaves, even though only arrays and
objects can have children. The candidate restricts that expansion to array
and object nodes. Scalar leaves remain in the walk and token stream, so numeric
type/text restoration, JSON Pointer paths, document ordering, and reconstructed
values remain unchanged. This does not validate stored documents or prune
document subtrees; consistency of stored JSON and metadata remains the caller's
responsibility.

The change applies to direct, flattened, and relation-backed JSONB documents
with a metadata column. It adds no binding API. It does not add metadata support
to native composite attributes; `BindComposite` has no per-attribute metadata
option.

## Applicability

| Physical placement / query | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Direct `BindJSON` document root | Applied | Scalar leaves are not expanded during recursive restoration. |
| Direct `BindJSON` nested scalar path | Partial | Leaf expansion is skipped; existing direct-binding selected-subtree restoration remains the larger optimization. |
| `BindJSON` child of a flattened `BindStruct` | Partial | Leaf expansion is skipped, but restoration still processes the full bound document. |
| `BindJSON` in a relation item binding | Partial | Leaf expansion is skipped, but restoration still processes the full bound document. |
| Native composite attribute with separate metadata | Not applicable | There is no per-attribute metadata binding API in `BindComposite`. |
| Native JSON or JSONB without metadata | Not applicable | No JSONB numeric metadata walk is needed. |

The direct nested path and the flattened/relation paths are not equivalent
measurements: direct `BindJSON` already supports selected-subtree restoration,
while flattened and relation bindings still restore the full document. The
candidate’s leaf filter applies across all three supported placements.

## Reproduction

The baseline is [4970c56](https://github.com/ornew/hff/commit/4970c5614ce321e9f51b46eca909a0f69a74cc1f). The candidate was measured as a source snapshot; its SHA-256 inputs are listed here.

| File | SHA-256 |
|:--|:--|
| `backends/sql/json_metadata.go` | `881c94639970f06c3eef37755ac18e0f514455bcdfa12b8a9c93475d0e52d452` |
| `backends/sql/metadata_placement_bench_test.go` | `96745be9771414115cc94aedb2dbd3bd084782f6d124a86cd6ebd081aaa1e2bc` |
| `backends/sql/metadata_placement_execution_bench_test.go` | `c276f52f5f9ee85540c23330c6e8a859a04cf460c8c6b1874646b3cb901d27ca` |

The host was Darwin 25.2.0, `darwin/arm64`, Apple M3 Max with 64 GiB RAM. Go
was 1.27.1 and benchmark output used the default `GOMAXPROCS=16`. PostgreSQL
was 18.6 in a Debian aarch64 container using image digest
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`, with
JIT disabled, `max_parallel_workers_per_gather=0`, forced custom plans, and a
60-second statement timeout. The database directory used tmpfs. There were no
explicit container CPU, memory, or cpuset limits.

The matrix covered three physical placements (Direct, Flattened, Relation),
two queries (`doc:type="object"` and `doc.nested.price>10.5`), and 100- and
1,000-row fixtures. The relation fixture had one child row per parent and an
index on `parent_id`; the `any()` predicate did not require an order contract.
All placements used rows from `metadataBenchmarkFixture`, including nulls,
negative zero, and unused nested payload fields. Before `EXPLAIN`, each sample
compared exact SQL result IDs against local HFF matches and checked the plan's
actual row count.

Build command:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^$' -bench '^BenchmarkPostgreSQLMetadataPlacementBuild$' -benchtime=200ms -count=3
```

PostgreSQL execution command (use a distinct plan directory for baseline and
candidate):

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> HFF_POSTGRES_PLAN_DIR=/private/tmp/hff-placement-plans GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^$' -bench '^BenchmarkPostgreSQLMetadataPlacementExecution$' -benchtime=1x -count=3
```

Raw logs, plans, SQL, exact IDs, and summary are outside Git in
`/private/tmp/hff-placement-evidence/` on the measurement host. The candidate
and baseline used the same harness and fixtures; the table reports only the
final three-sample pair after an initial serialized pair.

## Results

Times are medians of three samples; parentheses show min–max. Build `B/op` is
cumulative Go allocation, not peak or retained memory. The observed Build times
do not establish that the source change caused them. Allocation counts were
unchanged; `B/op` changed by −1 to +635 bytes and generated SQL by 59 bytes in
these cases.

| Placement / query | Baseline ns/op (min–max) | Candidate ns/op (min–max) | B/op | allocs/op | SQL bytes |
|:--|--:|--:|--:|--:|--:|
| Direct/DocumentRoot | 23651 ns (23503–25763) | 20864 ns (20727–21887) | 31,880 → 31,879 | 165 → 165 | 2,101 → 2,160 |
| Direct/NestedScalar | 36283 ns (35165–36735) | 29702 ns (29488–29806) | 69,031 → 69,666 | 272 → 272 | 6,812 → 6,871 |
| Flattened/DocumentRoot | 26466 ns (26422–29125) | 23491 ns (23297–23589) | 36,897 → 37,152 | 220 → 220 | 2,176 → 2,235 |
| Flattened/NestedScalar | 45092 ns (44658–45496) | 37702 ns (37519–37905) | 79,791 → 80,039 | 470 → 470 | 6,985 → 7,044 |
| Relation/DocumentRoot | 31846 ns (28999–35641) | 27484 ns (27384–27551) | 55,279 → 55,531 | 261 → 261 | 2,501 → 2,560 |
| Relation/NestedScalar | 41521 ns (40020–43066) | 37972 ns (37950–38024) | 120,701 → 120,959 | 333 → 333 | 7,314 → 7,373 |

### PostgreSQL planning and execution

| Placement / rows / query | Baseline planning ms (min–max) | Candidate planning ms (min–max) | Baseline execution ms (min–max) | Candidate execution ms (min–max) | Plan nodes | SQL bytes | Matched |
|:--|--:|--:|--:|--:|--:|--:|--:|
| Direct / 100 rows / DocumentRoot | 0.222 ms (0.216–0.26) | 0.213 ms (0.208–0.23) | 16.73 ms (16.59–16.98) | 12.93 ms (12.59–12.94) | 24 → 24 | 2,101 → 2,160 | 94 |
| Direct / 100 rows / NestedScalar | 0.457 ms (0.392–0.462) | 0.488 ms (0.459–0.506) | 0.805 ms (0.791–0.849) | 0.728 ms (0.669–0.825) | 43 → 43 | 6,812 → 6,871 | 79 |
| Direct / 1000 rows / DocumentRoot | 0.24 ms (0.231–0.316) | 0.27 ms (0.258–0.272) | 166.6 ms (166.1–168) | 126.7 ms (126–127.6) | 24 → 24 | 2,101 → 2,160 | 941 |
| Direct / 1000 rows / NestedScalar | 0.496 ms (0.444–0.578) | 0.445 ms (0.425–0.498) | 7.607 ms (7.493–8.031) | 6.159 ms (6.155–6.759) | 43 → 43 | 6,812 → 6,871 | 793 |
| Flattened / 100 rows / DocumentRoot | 0.289 ms (0.226–0.301) | 0.283 ms (0.216–0.333) | 16.7 ms (16.63–17.04) | 12.82 ms (12.74–12.92) | 24 → 24 | 2,176 → 2,235 | 94 |
| Flattened / 100 rows / NestedScalar | 0.449 ms (0.435–0.494) | 0.426 ms (0.417–0.428) | 17.78 ms (17.43–17.97) | 13.25 ms (13.19–13.5) | 47 → 47 | 6,985 → 7,044 | 79 |
| Flattened / 1000 rows / DocumentRoot | 0.251 ms (0.238–0.289) | 0.286 ms (0.249–0.323) | 165.7 ms (164.6–166.2) | 123.6 ms (123.3–124.2) | 24 → 24 | 2,176 → 2,235 | 941 |
| Flattened / 1000 rows / NestedScalar | 0.571 ms (0.5–0.582) | 0.5 ms (0.474–0.58) | 171.5 ms (170.5–173.9) | 133.8 ms (132.4–135.2) | 47 → 47 | 6,985 → 7,044 | 793 |
| Relation / 100 rows / DocumentRoot | 0.349 ms (0.299–0.405) | 0.307 ms (0.28–0.589) | 16.82 ms (16.75–17.52) | 12.65 ms (12.55–12.71) | 30 → 30 | 2,501 → 2,560 | 94 |
| Relation / 100 rows / NestedScalar | 0.512 ms (0.498–0.53) | 0.509 ms (0.472–0.614) | 17.56 ms (17.38–17.64) | 13.51 ms (13.5–13.63) | 53 → 53 | 7,314 → 7,373 | 79 |
| Relation / 1000 rows / DocumentRoot | 0.328 ms (0.32–0.365) | 0.324 ms (0.309–0.328) | 164 ms (164–168.2) | 123 ms (122.2–124.6) | 30 → 30 | 2,501 → 2,560 | 941 |
| Relation / 1000 rows / NestedScalar | 0.537 ms (0.526–0.562) | 0.557 ms (0.549–0.577) | 174.6 ms (170.8–175.1) | 133.8 ms (132.1–135.1) | 53 → 53 | 7,314 → 7,373 | 793 |

All sampled execution medians were lower for the candidate. For 1,000 rows,
Direct/DocumentRoot changed from 166.6 to 126.7 ms, Flattened/DocumentRoot from
165.7 to 123.6 ms, and Relation/DocumentRoot from 164.0 to 123.0 ms. For the
nested scalar query, Direct changed from 7.607 to 6.159 ms, Flattened from
171.5 to 133.8 ms, and Relation from 174.6 to 133.8 ms. These are measurements
of this fixture and environment, not a general speed or index-use guarantee.

## Correctness and tradeoffs

The change preserves every walk row needed for numeric type restoration and
JSON reconstruction, including scalar tokens, pointers, and token ordering.
The metadata contract and physical binding APIs are unchanged. Existing focused
metadata PostgreSQL tests cover numeric text restoration, selected subpaths,
nested bindings, and alias scopes. The execution harness checked exact result
IDs against local matching for each measured sample. The full PostgreSQL
integration suite, repository checks, and integration-tagged vet passed for the
measured candidate.

## Decision and limits

The candidate is retained as a bounded recursive-walk reduction for direct,
flattened, and relation-backed JSONB metadata documents. It does not prune
subtrees or establish a benefit for other database versions, schemas, row
counts, or document shapes. Flattened and relation nested scalar accesses still
restore the complete bound JSON document; that larger opportunity is outside
this comparison.
