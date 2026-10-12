# 002. Restore only selected JSONB metadata subtrees

- **Status**: Measured
- **Date**: 2026-10-12

## Problem and mechanism

The PostgreSQL JSONB binding can use a separate version-1 JSON Pointer map to
recover HFF numeric kinds and values that JSONB storage may normalize. Before
this change, a query reading one fixed nested field could recursively restore
metadata for the bound document. The translator now extracts the selected
subtree first and restores numeric metadata using the original document's
pointer prefix.

This applies only to a fixed, unguarded field chain rooted at a variable whose
nearest binding is a direct metadata-backed JSONB `BindJSON`. Whole-document
reads and paths whose resolution depends on a guard, computed receiver,
dynamic index, callback parameter, or nested physical binding retain the
general restoration path. A fixed field prefix that can be extracted before a
later guard remains eligible. Within a callback, an outer `doc.price` path can
qualify; a path rooted at the callback item does not. The caller's storage and
metadata consistency contract is unchanged; the build does not read or
validate database rows.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| PostgreSQL JSONB with version-1 type metadata | Applied | Fixed unguarded field subpaths can restore from the selected subtree. |
| PostgreSQL native JSON | Not applicable | Native JSON uses a separate lowering path and was only a Build control here. |
| Other JSONB paths | General fallback | Whole-document and ineligible chains retain recursive restoration; another eligible path in the same query can still be pruned. |

## Reproduction

The comparison used baseline 90abfb88f4ffe6c461c15253c7836111a6dd121f and
candidate 3305f7eb002453ed499fb971ecb4f21a58b50003. The benchmark harness is
ece0c9f. The candidate source patch captured during measurement was verified
byte-for-byte against the integrated candidate commit.

The host was a Mac15,9 with Apple M3 Max (arm64, 16 CPUs, 64 GiB), running
macOS and Go 1.27.1 (`darwin/arm64`). The Docker server was version 29.8.2;
its VM had 16 CPUs and 27.36 GiB. PostgreSQL ran in a network-isolated arm64
container from image
`sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`
(PostgreSQL 18.6, Debian, UTF-8, locale `C`). JIT and parallel workers were
disabled, and custom plans were selected. The container data directory used
tmpfs; no CPU affinity was set. The fixture used 1,000 rows with nullable
documents, signed zero, five-element numeric lists, and unused payload fields.
Every SQL sample checked matching row IDs against local HFF evaluation before
measuring the database plan.

Build measurements ran once per sample with:

```sh
GOTOOLCHAIN=go1.27.1 GOMAXPROCS=1 \
go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataBuild$' \
  -benchtime=200ms -count=1 -cpu=1
```

Database measurements used three alternating baseline/candidate samples. Each
execution benchmark separated PostgreSQL planning and execution through
`EXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS)`. The exact-ID
validation query preceded `EXPLAIN`, warming the scan; fixture loading and
validation time are outside the reported server timings:

```sh
GOTOOLCHAIN=go1.27.1 GOMAXPROCS=1 \
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=<unique-output-directory> \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLMetadataExecution/JSONBMetadata/1000Rows$' \
  -benchtime=1x -count=1 -cpu=1
```

The raw logs, plans, SQL, row IDs, environment capture, and runner are in
`/private/tmp/hff-metadata-pruning-20261012T114141Z` on the measurement host.
They are not versioned in the repository.

The harness compared these four HFF queries:

| Workload | Query |
|:--|:--|
| Scalar | `doc.price>10.5` |
| Repeated | `doc.price>10.5 doc.price<90 doc.price!=25 doc.price=doc.price:[float]` |
| Guards | `doc.price:[float]>10.5 doc.items:[list[float]]:size>=4` |
| Callback | `doc.items:[list[float]]:any(x=>x>3) doc.price>10.5` |

## Results

Values are medians of three samples. Ranges show the minimum and maximum
sampled database execution time or Build time. Build `B/op` is cumulative Go
allocation, not peak or retained memory.

| JSONB workload (1,000 rows) | Execution ms/op, baseline → candidate | Planning ms/op, baseline → candidate | Plan nodes, baseline → candidate | SQL bytes, baseline → candidate |
|:--|--:|--:|--:|--:|
| Scalar | 89.23 (88.81–91.96) → 5.317 (5.293–5.476) | 0.395 (0.333–0.419) → 0.286 (0.284–0.333) | 32 → 30 | 3,939 → 3,890 |
| Repeated | 101.4 (101.1–109.3) → 15.89 (15.70–16.14) | 0.812 (0.805–0.858) → 0.803 (0.748–0.809) | 62 → 60 | 11,477 → 11,428 |
| Guards | 95.35 (93.63–95.41) → 25.82 (25.53–31.81) | 0.424 (0.406–0.464) → 0.514 (0.505–0.666) | 46 → 62 | 4,179 → 5,936 |
| Callback | 101.5 (100.7–107.8) → 30.41 (29.96–31.55) | 0.568 (0.554–0.590) → 0.686 (0.681–0.722) | 60 → 76 | 6,490 → 8,250 |

| JSONB Build workload | ns/op, baseline → candidate | B/op, baseline → candidate | allocs/op, baseline → candidate | parameters, baseline → candidate |
|:--|--:|--:|--:|--:|
| Scalar | 27,668 (27,562–28,033) → 26,458 (26,203–27,009) | 41,044 → 42,140 | 247 → 216 | 2 → 3 |
| Repeated | 46,384 (46,313–47,554) → 45,815 (45,604–48,195) | 83,992 → 85,168 | 585 → 554 | 4 → 5 |
| Guards | 36,723 (35,599–36,974) → 35,960 (35,919–36,238) | 46,916 → 54,485 | 433 → 401 | 4 → 6 |
| Callback | 44,577 (43,488–44,683) → 43,207 (43,204–44,508) | 65,606 → 76,456 | 558 → 526 | 4 → 6 |

Build SQL bytes were 3,939→3,890 (Scalar), 11,477→11,428 (Repeated),
4,179→5,936 (Guards), and 6,490→8,250 (Callback). Thus SQL grew for Guards
and Callback while remaining similar for the other workloads. Build allocation
volume increased in all four cases even as allocation counts fell; these are
tradeoffs, not evidence that translation uses less memory or produces smaller
SQL.

Native JSON was a Build-only control. Its SQL, parameters, allocated bytes, and
allocation counts were unchanged by the JSONB-specific change; database
execution for native JSON was not measured in this comparison. Baseline and
candidate JSONB executions used the same typed fixtures, and their matching
IDs agreed with local evaluation (793 rows for Scalar, Guards, and Callback;
695 for Repeated).

## Correctness and tradeoffs

The captured plans and SQL show the selected-subtree lowering and changed
planning shape; they do not establish an index choice or execution behavior
for other data distributions.

## Decision and limits

The optimization is retained for the eligible JSONB path. In this controlled
workload, measured PostgreSQL execution time was lower in all four query cases,
while Build allocation volume and SQL size increased in some cases. These
three paired samples describe one local host, PostgreSQL image, fixture, and
query set. They do not predict production latency, other plans, other storage
profiles, or broader query shapes. Re-measure representative application data
before making a deployment decision.
