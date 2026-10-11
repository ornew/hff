# PostgreSQL Execution Baseline

This integration benchmark records PostgreSQL-reported planning and execution
time for a small controlled JSON/JSONB scan workload. It is a baseline for
later comparison, not a claim about representative services, index use, or
overall database performance.

## Reproduce the benchmark

Use Go 1.27.1 and a dedicated PostgreSQL 18.6 container. The benchmark runs
PostgreSQL through `docker exec` and `psql`; provide the container name or ID.
It creates and loads a temporary table, runs `ANALYZE`, prepares each predicate,
and executes `EXPLAIN (ANALYZE, FORMAT JSON, TIMING OFF, BUFFERS, SETTINGS)`.
JIT is disabled, parallel workers are set to zero, custom plans are forced,
and the temporary table has no indexes. The reported planning and execution
metrics come from PostgreSQL's `EXPLAIN` output; Docker/psql transport and
fixture setup are not included in those server-reported values.

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLJSONExecution$' -benchtime=1x -count=3
```

The 12 cases cross JSON and JSONB storage, 100 and 1,000 rows, and scalar,
`any()`, and `foldl()` predicates. For each case, the benchmark compares the
number of SQL rows returned with local HFF matches. It reports input and matched
row counts, plan-node count, generated SQL bytes, and median-able server
planning/execution milliseconds. This correctness check establishes the
measured result cardinality only; it does not replace the broader semantic
parity suite.

Set `HFF_POSTGRES_PLAN_DIR` to save the first sample's JSON plan and reproducible
SQL script for each case. For example:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/tmp/hff-postgresql-plans \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQLJSONExecution$' -benchtime=1x -count=3
```

## One measured baseline

One three-run sample used Go 1.27.1 on Darwin arm64 with an Apple M3 Max and
PostgreSQL 18.6. Each displayed time is the median of the three PostgreSQL
planning or execution measurements. The fixture's matched-row count is the
local HFF result; plan nodes and SQL bytes are constant across repeats for each
case.

| Storage | Rows | Predicate | Matched | Plan nodes | SQL bytes | Planning (ms) | Execution (ms) |
|:--|--:|:--|--:|--:|--:|--:|--:|
| JSON | 100 | Scalar | 60 | 12 | 748 | 0.154 | 0.176 |
| JSON | 100 | `any()` | 46 | 26 | 1,592 | 0.356 | 0.382 |
| JSON | 100 | `foldl()` | 38 | 41 | 2,526 | 0.737 | 1.139 |
| JSON | 1,000 | Scalar | 591 | 12 | 748 | 0.115 | 1.222 |
| JSON | 1,000 | `any()` | 461 | 26 | 1,592 | 0.354 | 3.237 |
| JSON | 1,000 | `foldl()` | 369 | 41 | 2,526 | 0.682 | 9.752 |
| JSONB | 100 | Scalar | 60 | 12 | 750 | 0.150 | 0.113 |
| JSONB | 100 | `any()` | 46 | 28 | 1,597 | 0.336 | 0.345 |
| JSONB | 100 | `foldl()` | 38 | 41 | 2,529 | 0.747 | 1.129 |
| JSONB | 1,000 | Scalar | 591 | 12 | 750 | 0.119 | 0.818 |
| JSONB | 1,000 | `any()` | 461 | 28 | 1,597 | 0.395 | 2.514 |
| JSONB | 1,000 | `foldl()` | 369 | 41 | 2,529 | 0.738 | 9.806 |

These figures describe this schema, fixture, server configuration, and query
set only. The table has no indexes, so it says nothing about index selection or
index-friendly relation plans. Compare plans and result parity under the same
server version, data shape, and settings before drawing conclusions. Semantic
coverage for other structured and relational mappings remains separate work;
this baseline does not imply support for them.
