# Gate row-dependent PostgreSQL pipeline stages after filters

- **Status**: Implemented
- **Date**: 2026-10-15

## Problem and mechanism

In an `items()` map/filter pipeline, later callback stages can contain built-in
work that depends on the current item. Lowering that work before a filter can
put it outside the condition that excludes rows. Eligible work runs behind a
per-row gate, including its type checks and normalization. The gate uses an
entry barrier and an inactive-row-preserving `LEFT LATERAL` export. Nested
local bindings may be used, but nested-only work on constant data does not
qualify. The callback body must contain a method call or explicit type guard;
trivial value or Boolean forwarding gets no extra gate.

The filter predicate converts to Boolean: false, null, and no-value exclude a
row. A no-value from an earlier map remains a failure for the whole result,
even if a later filter excludes that row. Root or outer captures, custom
methods, and independent call subtrees stay in their original placement so
shared work is not forced into each row.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| PostgreSQL `items()` pipelines | Partial | Gates eligible row-dependent method calls and type guards after filters; trivial forwarding, captures, custom methods, and independent call subtrees retain their placement. |
| Local | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [d50c37ec](https://github.com/ornew/hff/commit/d50c37ecfa0ba6bdb52b38b584199bf55f4f6c7c).
The fixture uses Go 1.27.1 and PostgreSQL 18.6
with UTF-8 and `C`/`C` collation. The Go SQL-shape fixture uses a `labels`
`Map<String|null>` JSON binding and a `message` String bound to a text column.
The row-dependent query is:

```hff
labels:items:map(p=>p[1]):filter(v=>v:startswith("keep")):map(v=>v:matches("^expensive")):any(v=>v)
```

The versioned fixture is `backends/sql/json_pipeline_scope_integration_test.go`.
The same fixture was run from the baseline revision for comparison. A capture
control substitutes `message:matches(...)` for the local `v:matches(...)`
stage. Supplementary SQL and plain-plan outputs are outside the repository at
`/private/tmp/hff-stage-{local-baseline,plan-baseline}.sql` and
`/private/tmp/hff-stage-{local,capture,invariant}-candidate.{sql,txt}`.

The placement assertion is reproducible with:

```sh
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 \
  go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLPipelineStagePlanGatesCorrelatedWork$' -count=1 -v
```

## Results

| Workload | Baseline | Candidate | Change | Uncertainty |
|:--|--:|--:|--:|:--|
| Row-dependent guarded kernel placement | 0 guarded, 1 unguarded | 1 guarded, 0 unguarded | Guarded work moves behind the row gate | Prefix-matched plain-plan placement only; no execution count. |
| Captured root kernel control | — | 0 guarded, 1 unguarded | Captured work stays outside the gate | Prefix-matched plain-plan placement only; no execution count. |
| Captured root and independent method subtree SQL | Baseline | Byte-identical | No shared work is moved into each row | SQL shape only. |
| Filtered query correctness | — | 19,652 query-record comparisons | Passed | Focused parity fixtures, not a timing sample. |
| Float and nullable invocation controls | — | 92 Float boundary assertions; 12 `InvokeNull` cases | Passed | Focused correctness cases. |

Plain `EXPLAIN` on the tested plans verifies where guarded and unguarded
expressions are placed. It does not establish how often PostgreSQL evaluates
deparsed expressions. No execution timing, allocation, or runtime-cost
measurement was collected.

## Correctness and tradeoffs

The outer result preserves inactive rows and their presence state. Earlier
map no-value remains globally observable. A filter's false, null, or no-value
predicate excludes a row from later results, but does not erase an earlier
map failure. Captures and independent work remain shared at their original
scope. Additional per-row gates may add overhead when most rows pass.

## Decision and limits

Use the gate only when callback analysis proves the work depends on the current
item, possibly through a nested local binding, and the body contains a method
call or explicit type guard. Trivial forwarding, root/outer captures,
nested-only work on constant data, custom methods, and independent call
subtrees remain outside the gate. The evidence is SQL-plan placement and
focused correctness coverage;
there are no benchmark or `EXPLAIN ANALYZE` results, and no runtime-speed
claim follows. Concatenation, `flatmap()`, and fold transport are outside this
mechanism.
