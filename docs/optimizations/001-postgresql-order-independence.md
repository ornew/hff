# 001. Propagate Proven Order Independence in PostgreSQL Collections

- **Status**: Measured
- **Author**: @ornew
- **Date**: 2026-10-12

## Problem and mechanism

PostgreSQL relations are unordered unless the binding supplies an explicit
unique order. Some HFF consumers do not observe order, but an unconditional
ordered aggregation can add work and can inhibit a direct correlated plan.
The translator propagates a conservative order-independence proof through
selected list consumers and omits ordering only where the HFF result is
unchanged. It keeps separate materializations when a path is used in both
ordered and unordered contexts.

For the measured query, `items:map(x=>x.price):any(price=>price>30)`, the
candidate omits ordering for the order-blind result. The direct `any()` control
query remains unchanged. The proof does not alter local evaluation, promise SQL
short-circuiting, or guarantee an index plan.

## Applicability

| Execution profile | Applied / Partial / Not applied | Reason |
|:--|:--|:--|
| PostgreSQL relation `map(...):any(...)`, 1,000 parents × 32 items | Applied | Downstream `any()` observes membership, not mapped-list order. |
| Plain relation `any()` control | Applied, unchanged | Existing correlated predicate is a control workload. |
| Indexing, returned list order, observable float ties, or failure precedence | Not applied | The operation can observe source order, so the translator retains it or requires a unique order. |

## Reproduction

The measurements used Go 1.27.1 on Darwin arm64 / Apple M3 Max and PostgreSQL
18.6 (aarch64 Linux) in the pinned container image
`postgres@sha256:5a5a84b19854a9ffaa54082c166ff4ec27473a361e496e5ea167f298f2da9722`.
The fixture creates 1,000 parent rows and 32,000 related rows with an index on
`hff_related(parent_id)`. Each database measurement executes
`EXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS)`. The harness
checks matched-row count against local HFF matching.

Baseline source revision: `89d2fdacb0e4186b3aee72dc589c1419588bf8af`.
Candidate branch base: `7423fdfdffd4f6f761a79cfd73931a8c78609ef3`; measured
patch commit: `52eed3ad19c149858b46e9aba966471119f2f15f`. The equivalent
rebased candidate implementation in this worktree is
`6b8a302ad3947ecd184f643d10c8b731655189fc`.
Runs were serialized in alternating order baseline, candidate, candidate,
baseline, baseline, candidate. Each benchmark invocation used one measured
execution (`-benchtime=1x`); the table reports the median of three samples per
revision. No parallel request load was measured.

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
go test -tags=integration ./backends/sql -run '^$' \
  -bench 'BenchmarkPostgreSQLRelationExecution/1000Parents/32Items/(Any|MapAny)$' \
  -benchtime=1x -count=1
```

The alternating raw results, SQL, EXPLAIN plans, and provenance were retained
outside Git at `/private/tmp/hff-order-independence-final/`. Provenance records
the benchmark SHA-256
`8681b9a02730fe6dea2b23ef3cf90ef6919edd3d614a94300369dbdeff9940bc`, patch
SHA-256 `66459717ddd34dc5d4e8366da52ccadaf887bb815d7228a10e735f10737303e1`,
and the source benchmark revisions.

## Results

PostgreSQL reported these medians across the three alternating samples:

| Query | Baseline plan nodes / SQL bytes | Candidate plan nodes / SQL bytes | Baseline planning / execution (ms) | Candidate planning / execution (ms) |
|:--|--:|--:|--:|--:|
| `items:map(x=>x.price):any(price=>price>30)` | 38 / 2,585 | 36 / 2,523 | 0.225 / 83.60 | 0.249 / 79.32 |
| `items:any(x=>x.price>30)` control | 10 / 594 | 10 / 594 | 0.225 / 3.663 | 0.249 / 3.750 |

Each query returned 1,000 parent rows in both the local and PostgreSQL checks.
The map-to-any execution median was lower in this fixture, with two fewer plan
nodes and 62 fewer SQL bytes. The control query stayed the same shape and had
similar timings. Three samples are a small local observation, not a stable
service-level estimate. Planning, execution, allocations, and retained memory
are separate measurements; peak memory and production workloads were not
measured here.

## Correctness and tradeoffs

The parity suite also covers reversed relation/tag rows, nested indices,
null/no-value callback results, and queries that use the same relation in both
membership and index contexts. These checks ensure that dropping order on one
proven path does not alter a separately order-sensitive use. Full SQL/local
semantic parity and database behavior are tested independently of the timing
sample.

The optimizer may fall back to list materialization for guards, computed
relation lists, alias collisions, or contexts where order can affect output.
Database planning and execution remain PostgreSQL choices; an available index
is not guaranteed to be selected, and local `MaxSteps` or callback order is not
enforced by the SQL backend.

## Decision and limits

Keep the proof-based order propagation for the supported PostgreSQL subset.
Revisit it if new transformations or result types make order observable in a
previously accepted context. This measurement applies only to the pinned
fixture, database version, and query pair; it is not a general claim that
PostgreSQL relation queries are faster or use indexes.
