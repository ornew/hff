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

### Restore only a selected JSONB metadata subtree

For a fixed, unguarded field path rooted at a variable whose nearest binding is
a direct metadata-backed JSONB `BindJSON`, the translator can extract the
selected subtree before restoring numeric types. Metadata lookup retains the
pointer prefix relative to the original document, so nested numbers use the
same sidecar entries as whole-document restoration. Whole-document access and
paths whose resolution depends on a guard, computed receiver, dynamic index,
callback parameter, or nested physical binding retain the general restoration
path. A fixed field prefix that can be extracted before a later guard remains
eligible. Within a callback, an outer `doc.price` path can qualify; a path
rooted at the callback item does not.
Each selected extraction still needs an object check and projection; this
describes the generated SQL shape, not a measured speedup or PostgreSQL plan
guarantee. See the
[JSONB binding contract](json.md#storage-requirements-and-limits).
The measured workload and tradeoffs are recorded in [JSONB metadata
restoration](../../optimizations/002-postgresql-jsonb-metadata-restoration.md).

### Skip child expansion for JSONB scalar leaves

During recursive metadata restoration, scalar leaves remain in the walk for
numeric type/text restoration, pointer lookup, and reconstruction, but the
recursive term does not try to expand them as children. The same leaf filter
applies to direct, flattened, and relation-backed metadata documents. Direct
nested `BindJSON` already has selected-subtree restoration; flattened and
relation bindings still restore the full document. This reduces recursive
expansion work without pruning values or changing the metadata contract. The
measured profiles and tradeoffs are in [JSONB leaf expansion](../../optimizations/005-postgresql-jsonb-leaf-expansion.md).

### Reuse parsed JSON within a projection scope

Repeated `fromjson()` calls on the same projected receiver can reuse the
complete validated JSON result within one projection scope. Each call keeps
its own presence state. Callback and shadowed-alias scopes remain isolated;
different SQL expressions are not merged just because they contain equal
runtime text. Full-document syntax, Unicode, duplicate-key, and nested numeric
validation still runs before the value is exposed. See the
[measured parsing and validation comparison](../../optimizations/004-postgresql-json-validation-reuse.md)
for the workload, generated-SQL tradeoffs, and sample limits.

### Reuse a restored parent for overlapping paths

Within the same projection scope, a fixed descendant can reuse an unguarded
JSONB parent that has already been restored. The parent restoration uses
document-relative metadata pointers; the descendant is extracted from that
restored JSON text without a second metadata lookup. A direct nested `BindJSON`
can qualify within its binding boundary, and the most-specific physical
binding takes precedence. The translator does not hoist a parent that appears
later in the query. Guarded values are not shared, and callback and order-demand
projections keep separate caches. See the
[measured overlapping-path comparison](../../optimizations/003-postgresql-jsonb-overlap-reuse.md)
for the workload that benefits and the controls that do not.

### Use correlated existence checks for plain relation predicates

A plain relation path consumed by `any()` or `all()` can lower to correlated
`EXISTS` or `NOT EXISTS` directly, without first aggregating all related rows
into a JSON list. The caller's correlation predicate stays in the subquery's
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
