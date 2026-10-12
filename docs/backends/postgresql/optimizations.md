# PostgreSQL Translation Optimizations

The PostgreSQL backend lowers HFF programs to parameterized SQL. These
translation choices preserve HFF value, presence, null, and no-value behavior
while controlling generated SQL shape. They do not promise a PostgreSQL plan,
physical evaluation order, local runtime budgets, or a performance result for
other schemas and data.

## SQL translation choices

### Keep literals as bound parameters

HFF literals remain typed query parameters in `Query.Args()`. Scalar literals
are referenced directly rather than wrapped in one-row subqueries. Literal
null is SQL `NULL`; query values are not interpolated into SQL text. Computed
expressions and guards retain projection barriers where they need a named
value or a separate presence state.

### Project computed steps once

Computed expressions are lowered through relational `LATERAL` projections.
`OFFSET 0` barriers keep downstream SQL from recursively expanding earlier
expressions. A presence value proven to be true can remain a SQL constant;
dynamic presence from null/type guards remains explicit. The barriers describe
translation structure and do not constrain PostgreSQL's physical execution
order.

### Share plain paths within each projection scope

Repeated unguarded paths from scalar columns, schema-bound JSON/JSONB,
composites, flattened structs, and callback item fields can share a projection
within one scope. Callback paths are keyed by lexical binding identity, field
path, and type; captures, shadowed parameters, nested callbacks, and separate
scopes remain isolated. Guards, dynamic indexes, and method calls are not
cached as plain paths. The backend also omits a null check when the declared
type proves non-null, and an exact repeated type guard when its constraint
matches the inferred type. These choices assume stored database rows honor the
declared schema; `Build` does not inspect rows.

### Read only selected struct fields

For native composites, an unguarded field chain can read the selected native
attributes before converting the selected value. For flattened structs, it can
project only the selected binding while retaining explicit parent-presence and
parent-null expressions. A whole struct or guarded/dynamic access uses the
general conversion path when required. See [composite bindings](composites.md)
and [flattened struct bindings](flattened-structures.md) for representation
requirements.

### Use correlated existence checks for plain relation predicates

A plain relation path consumed by `any()` or `all()` can lower to correlated
`EXISTS` or `NOT EXISTS` directly, without first aggregating all related rows
into a JSON list. The caller's trusted correlation stays in the subquery's
`WHERE` clause. For flattened relation items, only callback-observed fields
need to be projected; a whole item is built only when the callback observes
that value. Guards, computed relation lists, or alias collisions can require
the general list-materialization path.

### Drop ordering only when the proof reaches the consumer

Relation and transformation order is preserved whenever the consumer can
observe it. The translator can omit an aggregate `ORDER BY` when an order
independence proof propagates through the relevant list expression—for
example, a mapped relation used only by `any()`, or a canonical integer/string
sort whose stable ties are indistinguishable. It keeps order where equal float
representations, indexing, returned sequence contents, or callback failure
precedence make it observable. Shared materializations used in both ordered
and unordered contexts remain separate.

The proof preserves HFF results; it does not promise database short-circuiting
or callback evaluation order. See the [measured order-independence record](../../optimizations/001-postgresql-order-independence.md)
for one controlled relation workload.

## Measure translation and database execution separately

Build benchmarks measure HFF-to-SQL translation, allocations, generated SQL
bytes, and parameters. They do not measure PostgreSQL planning or execution.
With Go 1.27.1, run:

```sh
go test ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQL(StructuredBuild|FlattenedBuild|RelationBuild)$' \
  -benchmem
```

For database measurements, the integration benchmark reports PostgreSQL's
planning and execution separately using `EXPLAIN`. Provide a dedicated tested
PostgreSQL 18.6 container; the harness verifies SQL result cardinality against
local HFF matches. It does not claim index use outside the measured schema or
measure Docker/fixture setup as database execution time:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQL(JSON|Relation)Execution$' \
  -benchtime=1x -count=3
```

Set `HFF_POSTGRES_PLAN_DIR` to save the first plan and SQL sample per case:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
HFF_POSTGRES_PLAN_DIR=/tmp/hff-postgresql-plans \
go test -tags=integration ./backends/sql -run '^$' \
  -bench '^BenchmarkPostgreSQL(JSON|Relation)Execution$' \
  -benchtime=1x -count=3
```

The [JSON/JSONB execution baseline](../../development/postgresql-execution.md)
uses unindexed temporary JSON tables. Relation cases use a controlled indexed
correlation fixture. Keep schema, data, settings, and PostgreSQL version fixed
when comparing runs; plan and latency conclusions are workload-specific.
