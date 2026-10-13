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

### Emit eligible scalar predicates directly

For eligible comparisons between a directly bound native scalar column and a
literal, the backend can retain the comparison as a direct column predicate.
Boolean composition, null tests, and scalar truth tests can use this path when
their leaves are eligible. Nullable fields keep explicit non-null tests;
unsupported expressions retain the general lowering. The measured fixture
shortened one predicate and removed its projection barriers, but no execution
speedup was measured. See [direct scalar predicate shape](../../optimizations/013-postgresql-scalar-predicate-shape.md)
for the exact fixture and its limits.

Direct native Float comparisons are also available for schema-valid finite
Float columns and literals, with scalar truth/null tests and same-Float-literal
`in()` covered by the same focused path. Mixed Int/Float comparisons, computed
expressions, guards, JSON, and structured values retain the general lowering.
The focused cases remove `OFFSET 0` barriers, but no plan or runtime-speed
claim follows from SQL shape alone. Plain `EXPLAIN` on an empty temporary
indexed Float table showed a Bitmap Index Scan for `factor<1.5`; this is a
fixture-specific plan observation, with no timing measurement. See [direct
native Float predicates](../../optimizations/018-postgresql-float-predicate-shape.md)
for the measured filters and evidence limits.

For JSON-backed scalar Float pairs, PostgreSQL consumes the original or
metadata-restored value instead of serializing to canonical JSON and decoding
again. The supported PostgreSQL Float model also removes the older exact
arithmetic templates and their repeated decomposition. In the measured
`String|Float` self-pair, generated `WHERE` text shortened from 6,570 to 1,308
bytes, with 16 to 4 projection barriers; plain plan inspection showed a
smaller plan tree. These are SQL-shape observations, not speed claims. Exact
Float decomposition remains in some structural sort keys. See the [native
Float lowering record](../../optimizations/023-postgresql-native-float-lowering.md)
and [Float model design](../../design/021-backend-native-float-semantics.md).

### Compose direct predicates in mixed boolean filters

Eligible native scalar leaves can remain in the outer `WHERE` when a filter
also contains predicates that need general lowering. Generic siblings share
one scalar subquery, which retains shared JSON parsing, metadata restoration,
or exact Float work. If planned generic scopes share a schema path or ancestor
path, a conservative check falls back to general lowering for the whole filter.
This also covers cheap native columns and can miss some safe composition
opportunities.
Parameter binding remains in source order. This changes the generated SQL
shape, not HFF null/no-value semantics or PostgreSQL's physical evaluation
order. The [mixed predicate composition record](../../optimizations/019-postgresql-mixed-predicate-composition.md)
shows three examples and a fixture-specific plan inspection; no execution-speed
or default-planner index-use claim is made.

### Prune numeric comparison branches excluded by JSON schema

Dynamic JSON comparisons emit only Int or Float branches admitted by the
compiled receiver type. An Any value or a receiver type that permits both
numeric subtypes keeps runtime dispatch; exact mixed Int/Float comparison
remains unchanged. Null/no-value and runtime category mismatch behavior are
preserved. The [schema-guided numeric dispatch record](../../optimizations/020-postgresql-schema-numeric-dispatch.md)
shows the measured SQL-shape differences; no runtime-speed or plan claim is
made.

### Prune impossible categories in dynamic JSON pair comparisons

For dynamic-versus-dynamic comparisons, the translator intersects the JSON
categories permitted by both compiled operand types. It can skip numeric
subtype branches or recursive container equality when no matching category
can occur on both sides. Existing stored-value normalization, JSONB metadata
recovery, and unsupported-union checks still run before category pruning. Any
retains its full category set, but a restricted opposite operand can still
narrow the intersection; Any-versus-Any stays generic. Comparisons between
reachable containers retain recursive equality, and Float normalization still
applies whenever a Float branch is possible. See the [dynamic pair category
pruning record](../../optimizations/021-postgresql-json-pair-category-pruning.md)
for SQL-shape examples and limits; no timing or speed claim is made.

### Specialize reachable JSON container-union equality

For finite compiled root unions in JSON-backed `=` and `!=`, recursive equality
can use compiled types at each stored position. This includes overlapping
alternatives such as `List<Int>|List<Float>`; lists compare by ordinal and
objects compare actual keys, retaining Map/Struct cross-shape behavior and
per-key Map fallback. Unconstrained `Any` leaves keep generic recursive
equality. Stored-value representability and JSONB metadata requirements remain
in force; consistency between stored values and sidecar metadata remains the
caller's responsibility. Partially overlapping schemas may still require
recursive metadata restoration. `in()`, `contains()`, and `unique()` retain
their existing equality paths. The
[container-union equality record](../../optimizations/024-postgresql-container-union-equality.md)
gives the shape matrix and limits; these SQL reductions are not execution-time
claims.

### Use PostgreSQL's native approximate Float model

Scalar Float comparisons avoid a canonical JSON serialize/decode round-trip.
Supported Float arithmetic, conversions, rounding, and formatting follow the
PostgreSQL `float.*` Profile details; extreme arithmetic magnitudes use the
documented checked fallback. Int remains exact and mixed Int/Float comparisons
remain exact over represented values. In the measured JSONB pair shapes,
`String|Float` self-comparison fell from 6,570 to 1,308 `WHERE` bytes and from
16 to 4 projection barriers; this is SQL-shape evidence only. Plain plan
inspection also showed fewer nested-loop nodes, without execution timing. Some
structural sort keys still use exact Float decomposition. See the
[native Float lowering record](../../optimizations/023-postgresql-native-float-lowering.md)
and [backend-native Float design](../../design/021-backend-native-float-semantics.md).

### Compare scalar JSON pairs without canonical JSON conversion

When two JSON-backed dynamic values can only compare as scalars, the translator
can consume their original JSON values or metadata-restored JSONB values
directly. Reachable numeric families are decoded once for the comparison.
Schema checks, JSONB metadata recovery, and unsupported-union checks still run
before this path. If matching list or object values are possible, structural
equality retains the normalized recursive path. See [scalar JSON pair
decoding](../../optimizations/022-postgresql-scalar-pair-decode.md) for the
SQL-shape and fixture-specific `EXPLAIN` evidence. No execution timing was
collected.

### Lower homogeneous scalar membership directly

For eligible same-type scalar membership, the translator can use SQL
`IN` rather than encoding and decoding each value through JSON. The direct
predicate path handles literal membership on supported native scalar columns;
the expression path also avoids JSON conversion for homogeneous native scalar
values. Null candidates do not match, nullable receiver behavior is preserved,
and mixed numeric equality stays on the exact general path. For the eligible
root filter `age:in(18,21)`, direct composition with scalar predicates
shortened the `WHERE` expression from 1,627 to 80 characters and removed all
three projection barriers; it binds two arguments. A same-type dynamic
membership expression is not measured here. For the same literal filter, an
intermediate native-`IN` lowering generated a 399-character `WHERE` and
retained three barriers; the direct root-predicate lowering generates 80
characters with no barriers and two arguments. Plain `EXPLAIN` on an empty
temporary table with an age index selected a Bitmap Index Scan for the direct
literal-root predicate. This plan result is fixture-specific; no execution
speed was measured. See
[homogeneous scalar membership](../../optimizations/014-postgresql-scalar-in.md)
for scope and limits.

### Emit eligible native String method predicates directly

For `contains()`, `startswith()`, and `endswith()` on a directly bound native
String column with literal String or null arguments, the translator can emit
the predicate directly without intermediate projections. It can also lower
`len()` comparisons against integer literals directly. These paths preserve
null handling and `COLLATE "C"`; computed, dynamic/Any, JSON, and nonliteral
argument shapes retain the general lowering. The measured predicates were
shorter and had no `OFFSET 0` barriers, but no plan or execution timing was
collected. The [native String predicate shape](../../optimizations/015-postgresql-string-predicate-shape.md)
record gives the exact filters and limits.

### Emit direct predicates for JSONB String lists

For a directly bound JSONB `List<String>`, `contains()` with one or more
literal String needles and the exact callback form
`tags:any(item=>item="auth")` can lower to `@>` without projection barriers.
For `contains()`, all needles must occur in the list, and duplicate needles do
not require extra occurrences. The callback path applies only when equality
uses the callback's own bound item; a callback parameter may shadow an outer
variable, but a captured/outer binding, complex body, guard, or type observation
uses the general lowering. Nullable containers retain an explicit null test;
null or dynamic needles and other list shapes also use the general path.

The measured `contains()` filter shrank from 1,164 characters and four
barriers to 84 characters and none; the measured `any()` filter shrank from
884 characters and seven barriers to the same 84-character predicate. Plain
`EXPLAIN` on an empty temporary fixture with default planner settings selected
Bitmap Index Scans for both `jsonb_ops` and `jsonb_path_ops`. These observations
do not establish production index selection or runtime speed; no execution
timing was collected. See the [direct JSONB String-list membership](../../optimizations/016-postgresql-jsonb-string-list-membership.md)
and [simple JSONB String-list any](../../optimizations/017-postgresql-jsonb-string-list-any.md)
records for eligibility, parity, and limits.

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

For a fixed literal field chain, the translator can extract a selected subtree
before restoring numeric types. Direct metadata-backed `BindJSON` has this
support; it also applies when the chain crosses a flattened `BindStruct` or
reads a JSONB document in a relation item's physical binding. Parent
presence/null state is retained, and metadata lookup uses the subtree's
document-relative JSON Pointer. Whole-document reads and paths whose
resolution crosses a guard, computed value, or dynamic index retain the
general restoration path. Callback projection scopes remain isolated, while a
fixed path rooted at a physically bound relation item can qualify within its
own scope. Each extraction still needs an object check and projection; this
describes SQL shape, not a plan or runtime guarantee. The caller remains
responsible for stored document/metadata consistency; no per-attribute
metadata API is added. See the [JSONB binding contract](json.md#storage-requirements-and-limits),
[direct-binding measurements](../../optimizations/002-postgresql-jsonb-metadata-restoration.md),
and [flattened/relation measurements](../../optimizations/010-postgresql-structured-metadata-subtrees.md).

### Skip child expansion for JSONB scalar leaves

During recursive metadata restoration, scalar leaves remain in the walk for
numeric type/text restoration, pointer lookup, and reconstruction, but the
recursive term does not try to expand them as children. The same leaf filter
applies to direct, flattened, and relation-backed metadata documents.
Independently, fixed field chains under flattened and relation bindings can
use selected-subtree restoration as described above; other paths may still
restore the full document. Scalar-leaf filtering does not validate stored
documents or change the metadata contract. The measured profiles are in
[JSONB leaf expansion](../../optimizations/005-postgresql-jsonb-leaf-expansion.md).

### Reuse fixed Float SQL fragments during Build

This was a Build-allocation optimization for the earlier checked/unchecked
Float SQL templates. The native Float model replaced those templates, so this
mechanism is no longer active. The record retains the historical measurements:
[Float Build fragments](../../optimizations/006-postgresql-float-build-fragments.md).

### Reuse repeated Float arithmetic operands

This was a SQL-scope cache for the earlier exact Float arithmetic
decomposition. The native Float model replaced that arithmetic path; exact
decomposition remains in some structural sort keys. The record retains the
historical measurements: [Float operand reuse](../../optimizations/008-postgresql-float-decomposition-reuse.md).

### Sort recoverable dynamic JSON lists

For native JSON `Any` lists, dynamic sorting can consume the original array
after checking that the receiver is an array, instead of rebuilding a
comparison document. JSONB keeps comparison-document normalization when
numeric subtype restoration requires it. The dynamic Float key is projected
inside the item query; zero uses an exact numeric zero key without the shared
Float decomposition. The homogeneous schema-resolved fast path and global
Float arithmetic lowering are unchanged. Stable ordering, invalid-family
checks, and representation recovery remain in place. The combined candidate
was measured on fixed JSON/JSONB fixtures; its result is workload-specific and
does not promise lower execution time for every shape. See the
[dynamic JSON sort measurements](../../optimizations/011-postgresql-dynamic-json-sort.md)
for controls, ranges, and limits.

### Prune scalar leaves during canonical JSON serialization

The recursive token CTE keeps scalar and null rows as token sources but does
not expand them as children. Float tokens project their decoded value once
before canonical text formatting. This reduces generated SQL for the measured
dynamic JSON shapes; it does not guarantee faster execution for every query or
enforce the local JSON-depth limit. See [canonical JSON lowering]
(../../optimizations/009-postgresql-canonical-json-lowering.md) for the
workloads, controls, and limits.

### Specialize recoverable JSON equality by declared root type

When both operands have definite roots in the same structured category, the
translator uses typed list or object recursion even when nested values are
`Any`. An `Any` value compared with a known scalar also uses the known scalar
category and comparison. Comparisons between two unconstrained roots, an
unconstrained root and a container, and ambiguous structural unions keep the
generic recursive path. Numeric representability and JSONB metadata checks
still apply to both operands. See the [measured JSON equality comparison]
(../../optimizations/007-postgresql-guided-json-equality.md) for workload and
control results; it does not promise a performance gain for other schemas.

### Reuse parsed JSON within a projection scope

Repeated `fromjson()` calls on the same projected receiver can reuse the
complete validated JSON result within one projection scope. Each call keeps
its own presence state. Callback and shadowed-alias scopes remain isolated;
different SQL expressions are not merged just because they contain equal
runtime text. Full-document syntax, Unicode, duplicate-key, and nested numeric
validation still runs before the value is exposed. See the
[measured parsing and validation comparison](../../optimizations/004-postgresql-json-validation-reuse.md)
for the workload, generated-SQL tradeoffs, and sample limits.

### Read duration fraction digits as bytes

The duration parser validates its input grammar before processing fractional
digits, then converts the validated ASCII fraction to UTF-8 bytes once and
reads digits by byte offset while carrying exact decimal sums. Paired
measurements show much lower PostgreSQL execution time for the tested long
fractions, with increased Build size/allocation and a small regression on
short compound inputs. The result is specific to the measured profile. See
the [duration parser measurements](../../optimizations/012-postgresql-duration-parser.md)
for ranges, controls, and limits.

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
