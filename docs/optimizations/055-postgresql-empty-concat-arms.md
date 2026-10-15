# 055. Prune statically empty PostgreSQL list-concat arms

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

PostgreSQL list concatenation can produce SQL for an arm whose row stream is
known to be empty, or retain an append when only one useful stream remains.
For supported JSON/JSONB shapes, the builder proves a subset of arms empty and
omits their row sources from the generated SQL. Source expressions remain
evaluated in the emitted query. The builder also lowers full expressions for
Build validation; only discarded normalization and callback validation happen
in an isolated pass, so they do not leave unused SQL parameters. When all arms
are empty, the result remains the correctly typed empty list.

The initial proof covers literal empty lists, `List<Never>`, `Map<Never>`
enumeration, zero-input `map()`, `filter()`, `flatmap()`, `sort()`, or `distinct()`,
literal-false filters, and concat trees whose arms are all explicitly proven
empty. It applies to ordinary/dynamic JSON concat and the bounded item-slot
map/tree pipeline. The generic deferred-list follow-up applies when a
statically empty branch remains available to a compatible materialization,
aggregate, or fold consumer; this path does not infer emptiness from computed
`Never` schema flags. It omits proven-empty rows, counts, sentinels,
selections, encodings, runtime packet rows, or fold successors while
retaining static packet variants and original arm identities. An empty list
returned by a callback for a nonempty parent is not statically discarded
because its failure sentinel is observable. Runtime-created empty streams
without an explicit proof, and unproven generic deferred-list paths, keep
their existing lowering.

All operands and callbacks are lowered and validated before output is chosen;
receiver-first diagnostic ordering, capability checks, null/no-value
precedence, and the original outer presence/null state remain intact. In item
pipelines, an earlier map's no-value cannot be erased by an empty later
filter; a null concat result still masks failures from maps after that concat.
The optimization does not change the HFF concat contract.

## Applicability

| Shape | Status | Reason |
|:--|:--|:--|
| Statically empty JSON/JSONB concat arm in supported ordinary/dynamic concat | Applied | The arm is fully validated, then its empty row stream is omitted. |
| Proven empty arm in the supported item-slot concat map/tree pipeline | Applied | The item-slot stream's schema, order, outer state, and no-value checkpoints remain intact. |
| Proven empty arm carried through a supported generic deferred-list consumer | Applied | Empty rows and payloads are omitted while presence, null/no-value state, and packet arm identities remain. |
| Concat tree with a sole surviving stream | Applied | The normalized stream is forwarded in its original order and presence/null context. |
| Runtime-stage-created empty stream or unproven deferred-list path | Not applied | The existing path retains runtime validation and any state the static proof cannot establish. |
| Local backend | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The initial ordinary and item-slot baseline is
[68b8821](https://github.com/ornew/hff/commit/68b8821a236eeda2104d94e1b0b14da33914052f).
The generic deferred-list follow-up baseline is
[85d1799](https://github.com/ornew/hff/commit/85d179937fae057e6eebdf78698ca19869729248).
The candidates are the source trees containing the corresponding changes.
Go 1.27.1 ran on Darwin arm64 with PostgreSQL 18.6.

The focused unit and PostgreSQL parity tests are:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestEmptyConcat' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLEmpty.*Concat' -count=1
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^TestEmptyDeferred' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLEmptyDeferredConcatMatchLocal$' -count=1
```

For the initial ordinary and item-slot scope, the final PostgreSQL run recorded
54,392 query/record results across 192
groups, including 7,396 ordinary concat outputs and 128 accepted dynamic
concat outputs from the new empty-arm tests. Four unsupported String-overload
cases were separately confirmed to fail during Build; omitted-OID parameter
checks also passed. The ordinary cases include empty literal arms and a single
surviving stream. Item-pipeline cases exercise empty streams in map/filter and
concat trees, null/no-value precedence, outer state, and callback validation.

Raw SQL-size and plain-plan outputs are retained outside the repository:

- `/private/tmp/hff-empty-concat-{before,final}.log`
- `/private/tmp/hff-empty-concat-pipeline-{before,after}.log`
- `/private/tmp/hff-empty-concat-{before,final}-explain.log`

## Results

| Workload | Baseline `WHERE` bytes | Candidate `WHERE` bytes | Change | Uncertainty |
|:--|--:|--:|--:|:--|
| Ordinary integer-list concat, `sum()` observer | 3,412 | 2,311 | −1,101 bytes | Generated SQL size only |
| Ordinary integer-list concat, JSON observer | 3,507 | 2,364 | −1,143 bytes | Generated SQL size only |
| Ordinary integer-list concat, `any()` observer | 3,474 | 2,373 | −1,101 bytes | Generated SQL size only |
| Item-slot pipeline map/type observation | 4,669 | 4,010 | −659 bytes | Generated SQL size only |
| Item-slot pipeline `any()` | 4,290 | 3,529 | −761 bytes | Generated SQL size only |
| Nested item-slot pipeline with unused callback | 6,253 | 5,048 | −1,205 bytes | Generated SQL size only |

For the first ordinary sum query, plain `EXPLAIN` changed from 34 to 23 plan
lines: Aggregate nodes went from 6 to 3, Function Scan nodes from 7 to 3, and
the Append node was removed. This is plan-shape evidence only. No
`EXPLAIN ANALYZE`, execution timing, allocation, or speed measurement was
collected. Eleven ordinary SQL and argument controls remained byte-identical.

The generic deferred-list follow-up passed 20,812 outputs for 121 sources
across 85 records plus one omitted-OID execution per source in both JSON
formats. Its selected regression run logged 211,426 query/record outputs
across 598 groups. That run excludes a sort-parity counterexample reproduced
on both baseline and candidate, so it does not establish full backend parity.
For four representative sources, the generated `WHERE` sizes were:

| Workload | Baseline `WHERE` bytes | Candidate `WHERE` bytes | Change | Uncertainty |
|:--|--:|--:|--:|:--|
| Deferred map, type observer | 5,535 | 5,090 | −445 bytes | Generated SQL size only |
| Deferred map, `any()` | 5,053 | 4,284 | −769 bytes | Generated SQL size only |
| Deferred `foldl()` | 6,871 | 6,642 | −229 bytes | Generated SQL size only |
| Deferred projected sum | 7,713 | 7,025 | −688 bytes | Generated SQL size only |

Across those four cases, statically false predicates fell from 2/2/1/1 to
zero. `UNION ALL` counts changed from 2/1/2/1 to 0/0/1/0; the remaining
recursive-fold union is required. The additional SQL-shape logs are retained at
`/private/tmp/hff-empty-deferred-{before,after}.log`; the four SQL files on
each side are `/private/tmp/hff-empty-deferred-{before,after}-{0..3}.sql`.

For the generic deferred-list map-type query, plain `EXPLAIN` changed from 78
to 77 lines, while Aggregate (4), Result (24), and Append (0) counts were
unchanged. PostgreSQL already pruned the dead rows in that plan. This is a
plan-shape observation, not evidence that the query executes less work.

## Correctness and tradeoffs

The validation pass can add Build work, while the emitted query avoids SQL for
proven empty arms. The mechanism preserves source/receiver validation and
checks that discarded callback expressions do not leave unused query
parameters. Tests cover literal and statically derived empty lists, all-empty
concat identities, single-stream forwarding, diagnostics order, cancellation,
null and no-value behavior, packet variants, fold successors, and JSON/JSONB
parity. Runtime-created empty streams and generic deferred-list paths without
a proof retain the existing lowering.

## Decision and limits

Use this optimization only when the current lowering proves an arm empty
without suppressing source evaluation, validation, or state. Preserve the
generic path when a deferred-list consumer cannot carry that proof. SQL-size
and plain-plan observations do not establish runtime speed, and no claim is
made for workloads with different schemas or data.
