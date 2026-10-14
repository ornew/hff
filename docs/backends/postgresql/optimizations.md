# PostgreSQL Translation Optimizations

The PostgreSQL backend lowers HFF programs to parameterized SQL. These
translation choices preserve HFF value, presence, null, and no-value behavior
while controlling generated SQL shape. They do not promise a PostgreSQL plan,
physical evaluation order, local runtime budgets, or a performance result for
other schemas and data.

## SQL translation choices

### Compare native Timestamp bindings by their physical columns

For eligible native `BindTimestamp` filter comparisons against a Timestamp
literal or another native bound Timestamp, the builder compares the physical
`timestamptz` base and optional 0–999 ns remainder directly. Native
`before()` and `after()` use this path as well. This avoids reconstructing
numeric Unix-epoch nanoseconds for these predicates while preserving the
storage and null contracts. Guards, structured/value expressions, and
JSON/JSONB Timestamp expressions keep their existing lowering. In the checked
indexed fixture, plain `EXPLAIN` used a Bitmap Heap Scan with a `BitmapOr` of
two Bitmap Index Scans for the direct predicate; the representative epoch
extraction expression used a sequential scan. This is plan evidence only, not
a timing result or a general planner guarantee. See the
[native Timestamp predicate record](../../optimizations/045-postgresql-native-timestamp-predicates.md)
for the paired parity cases and reproduction.

### Reuse bounded equality in JSON membership

For bounded List, Map, and Struct alternatives, `in()`, `contains()`, and
`unique()` can use schema-guided recursive equality after representation and
metadata checks. `Any` roots and nested `Any` leaves retain generic equality;
upstream normalization of a complete membership receiver also remains. The
measured SQL removed recursive document construction, though some barrier
counts increased. Plain `EXPLAIN` confirmed smaller plan trees in the tested
empty-table fixture; no execution timing was collected. See [bounded JSON
membership equality](../../optimizations/027-postgresql-schema-membership-equality.md)
for profiles, parity scope, and limits.

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
`in()` covered by the same focused path. Other mixed Int/Float comparisons,
general computed expressions, guards, JSON, and structured values retain the general
lowering.
The focused cases remove `OFFSET 0` barriers, but no plan or runtime-speed
claim follows from SQL shape alone. Plain `EXPLAIN` on an empty temporary
indexed Float table showed a Bitmap Index Scan for `factor<1.5`; this is a
fixture-specific plan observation, with no timing measurement. See [direct
native Float predicates](../../optimizations/018-postgresql-float-predicate-shape.md)
for the measured filters and evidence limits.

### Remove projections from eligible native-column filters

For eligible root filters, native `exists()`, same-kind bound-column
comparisons, `bytea` `len()` against an Int literal, Duration nanosecond
comparisons, and Int rounding identities can avoid nullable value projections.
Required `exists()` can become `TRUE`; nullable values retain non-null tests.
Guarded, structured-parent, JSON, mixed-kind, and callback paths retain their
general lowering. The [native-column filter record](../../optimizations/038-postgresql-native-column-filters.md)
reports SQL shape and parity evidence without claiming execution speed.

### Reuse qualified raw columns in generic expressions

When a native scalar binding has a qualified SQL column reference and its
expression resolves to the exact registered column with constant-true no-value presence, generic
expressions can reuse that column instead of projecting the raw value again.
Nullable and optional fields qualify too; SQL NULL remains NULL.
This removes leaf projections but leaves projections for computed methods,
guard results, JSON, Timestamp, and structured bindings. Reuse
stays within existing projection scopes; it does not authorize sharing across
callback parameters or shadowed bindings. The [qualified native-column
projection record](../../optimizations/039-postgresql-qualified-column-projection-reuse.md)
reports SQL-shape and plan observations, not execution timings.

### Reuse repeated native operation results

Within one projection scope, repeated native Int arithmetic or negation and
built-in String `lower()`/`upper()` can reuse a completed result when the
operation, typed operands, presence, and result type match. Operands are still
translated and validated in source order. Literal cache hits remove only
freshly added literal parameters; nonliteral operands must add no new parameters
for a hit to be reused.
Callback scopes remain isolated, and custom method implementations are never
evaluated to form cache keys. See the [native operation
reuse](../../optimizations/040-postgresql-native-operation-reuse.md) record
for filters, parity coverage, and SQL-shape measurements; no speed claim
follows from those measurements.

### Reuse native identity views and proven presence

Int- and Null-valued `round()`, `ceil()`, and `floor()` views and native
Duration `nanoseconds()` can reuse an already projected operand, preserving
its carrier and no-value state. Eligible arithmetic, collection, JSON access,
regex, and method expressions can also omit tautological conditions when a later
operand's presence is proven `TRUE`. Float rounding, dynamic JSON decoding, and guard-dependent presence
retain their computed paths. The [identity and presence record](../../optimizations/041-postgresql-native-identity-and-presence.md)
reports paired parity and SQL-shape evidence; no runtime-speed claim follows.

### Specialize exact mixed-numeric literal thresholds

For pure filter comparisons between a directly bound Int or Float column and
a typed literal of the other numeric kind, Build can derive an exact direct
threshold. For example, `i>18.5` becomes `i>=19`, while
`f>9007199254740993` becomes `f>=9007199254740994`; the compared column values
are not rounded. The rewrite covers all six comparisons and preserves nullable
column handling. Eligible identity-derived columns use the existing direct
path. Other computed, guarded, dynamic, JSON, and value-context operands
keep the general lowering; same-Float comparisons remain on the direct Float
path. Shape and plain-plan observations are in the [exact mixed-numeric
threshold record](../../optimizations/033-postgresql-exact-mixed-numeric-literal-thresholds.md).
They do not establish runtime speed. An indexed-fixture check with sequential
scans disabled showed candidate index conditions; it does not establish
default planner selection.

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

### Fold closed built-in conversions during Build

PostgreSQL `Build` resolves supported literal-only chains of exact built-in
method IDs into typed SQL values in eligible value positions. This preserves
source argument order and conversion failures without changing the compiled
HFF plan or using extensions as constant evaluators. Float-looking `number()`
input and Float operations stay on PostgreSQL's native path. See [literal
conversion folding](../../optimizations/025-postgresql-literal-conversion-folding.md)
for tested SQL shapes, accepted forms, and evidence limits.

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

### Prune numeric arithmetic branches excluded by JSON schema

For JSON-backed arithmetic, negation, and numeric rounding, the translator
intersects the compiled value type with the stored schema's possible numeric
families after the existing representation and metadata checks. A
`String|Int` value therefore has only an Int input family; Int division still
returns Float. A
`String|Float` value retains PostgreSQL's general Float kernel, and an
`Int|Float` or `Any` value keeps each numeric family it can reach. Int
overflow, Int-to-Float promotion, Int division results, and the PostgreSQL
native Float Profile remain unchanged. Parity and SQL-shape checks passed, but
no runtime measurement was made. See the [schema-guided arithmetic branch
record](../../optimizations/028-postgresql-schema-numeric-arithmetic.md) for
filters and limits.

### Keep single-family JSON arithmetic results typed

When a compiled JSON arithmetic expression has one non-null numeric result
family and all reachable branches return that family, the translator can
return `bigint` or `double precision` directly instead of serializing the
result as JSON and decoding it again. Representation checks, JSONB metadata
recovery, presence, typed SQL-null carriers, Int overflow and promotion, and
the PostgreSQL Float Profile remain in place. Int division still returns
Float. Results with multiple possible numeric families retain JSON; Null-only
and Never results keep their existing null carrier.
The [typed numeric result record](../../optimizations/029-postgresql-typed-numeric-results.md)
reports generated SQL and fixture-specific plain `EXPLAIN` structure; no
runtime timing was measured.

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

### Remove proven arithmetic identities

For statically known Int and Float expressions, PostgreSQL Build removes only
the proven literal-zero and positive-one identities. Required Int-to-Float
casts, signed-zero behavior, null/no-value handling, and callback scope remain
intact. Float addition by zero, dynamic numeric dispatch, multiplication by
zero, and reassociation retain their existing lowering. The [arithmetic
identity record](../../optimizations/026-postgresql-arithmetic-identities.md)
reports SQL and plain-plan shape only; no timing was measured.

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

### Reuse numeric comparison kernels within a scope

When a projection repeats mixed numeric comparisons, the translator can reuse
native Float floor and Int/Float ordering projections. Within one scalar JSON
pair, identical stored text can also share a decode when its numeric family,
ambiguity check, and Float override match. Operators, operand direction, and
presence remain comparison-specific, and caches do not cross callback, filter,
or independent JSON-pair scopes. The [numeric comparison kernel record](../../optimizations/032-postgresql-reuse-numeric-comparison-kernels.md)
reports generated-SQL and plain-plan shape only; no execution timing was
collected.

### Reuse validated JSON from map outputs

After full schema validation, a generated `map()` output can retain a
representation proof keyed by its exact SQL expression, native JSON format,
and captured stored type. Ordinary aliases and SQL-NULL-to-JSON-null
normalization preserve that proof; guards, type guards, extraction, and casts
do not. Presence and Float-loss handling remain separate, and canonical
serialization is unchanged. This reduces selected `json_array_elements`
expansion sites and corresponding Function Scan nodes. Projection barriers
and sampled Result/Nested Loop counts remain unchanged. See the
[normalized map JSON reuse](../../optimizations/034-postgresql-reuse-normalized-map-json.md)
record for the 11,648-case baseline/candidate parity matrix and limits; no
execution timing or memory measurement was made.

### Reuse normalized JSON across compatible fold states

When the fold seed and callback results normalize compatibly to the inferred
accumulator representation, the translator can reuse the predecessor's
normalized JSON proof through the recursive callback scope. The proof retains
the exact value, native JSON format, and original stored type; reuse against a
target schema requires a separate value-stability and full-schema check. Per-key
Map/Struct widening is reused only where covered by the proof. Incompatible
shapes keep ordinary normalization. This reduces selected
`json_array_elements` and `json_each` expansion sites; the measured
projection-barrier counts remain unchanged. No execution timing or memory
measurement was made. See the
[fold normalization proof](../../optimizations/035-postgresql-reuse-normalized-fold-json.md)
for source workloads, parity coverage, and limits. The fold matrix contains
17,216 PostgreSQL/local results on both baseline and candidate; it includes
Map-to-Struct widening and Float-format controls.

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

For `~`, `!~`, and built-in `matches()` in filter context, a directly bound
native String column with a literal pattern can use the translated PostgreSQL
operator directly. The builder binds only the translated pattern and keeps an
explicit null mask. Dynamic patterns, JSON/computed/guarded receivers, and
`matches()` in value contexts or callback bodies keep the general lowering.
An empty-table,
forced-index diagnostic showed the direct `~` predicate eligible for a
matching C-collated `pg_trgm` GIN index where the previous `regexp_like()`
shape was not. This is fixture-specific plan evidence, not a timing or
automatic-index-use guarantee. See
[literal regex filter lowering](../../optimizations/037-postgresql-literal-regex-filter.md)
for exact shapes and null behavior.

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

### Use containment for simple JSONB object and list filters

For a directly bound, metadata-free JSONB object, literal-key equality against
a String or Bool literal can use `column @> jsonb_build_object(key, value)`
with an explicit non-null filter. For a directly bound JSONB `List<String>`, a
simple `all()` equality callback can use `column <@ jsonb_build_array(value)`.
The list subset preserves empty-list truth and duplicate-insensitive
containment. Missing keys and JSON-null values are still handled by these
predicates and evaluate false for the non-null literal examples above. Null
equality, numeric or nested paths, JSON text, metadata-backed object bindings,
nullable list elements, and complex callbacks do not use these specialized
paths. See the [JSON guide](json.md#containment-for-simple-jsonb-filters) for
examples and exact limits.

On a temporary GIN-indexed JSONB column, plain `EXPLAIN` selected a Bitmap
Index Scan for object `@>`. List `<@` remained a Seq Scan even with sequential
scans disabled, so the checked list predicate did not expose a GIN index
condition. This is fixture-specific plan evidence only; it does not establish
runtime speed or production planner choices. See the
[JSONB containment record](../../optimizations/042-postgresql-jsonb-containment-predicates.md)
for parity coverage and the exact diagnostic.

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

### Select a JSON object value before normalization

For a schema-known Map, `get(key)` can validate the applicable Map schemas,
select the raw value, then decode or normalize only that candidate. A literal
key validates only its selected value schema, so unused Struct fields do not
add representation requirements. One known primitive category can decode
directly after a category check; selected unions and nested Maps retain
selected-value normalization. Dynamic access through heterogeneous Structs,
and `values()`/`items()`, still normalize the object. The
[selected Map value record](../../optimizations/030-postgresql-select-map-values-before-normalization.md)
reports SQL shape and controls; these measurements make no runtime-speed
claim.

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

Within a projection scope, repeated unguarded literal-field chains rooted at
the same built-in `fromjson()` result can also share selected extraction and
decode work. Reuse is keyed by the parsed expression, presence, format, types,
ordering class, and field path. Full-document validation still occurs.
Guarded, indexed, extension, computed-root, and cross-callback paths retain
their ordinary lowering. See the
[parsed JSON field reuse comparison](../../optimizations/036-postgresql-reuse-parsed-json-fields.md); the
measured `Function Scan` counts did not change, and no execution timing was
collected.

### Reuse constructed primitive JSON in callbacks

For `map()` and `foldl()`/`foldr()`, a newly computed native SQL value can be
encoded directly into callback JSON state when its complete stored schema is
one primitive family, optionally nullable, matching the SQL carrier.
SQL `NULL` becomes JSON `null`; presence and no-value remain separate. This
avoids parsing and normalizing the just-constructed scalar again. JSON
carriers, `Any`, wider unions, containers, and `flatmap()` results retain
schema-guided normalization.
The measured Float map and fold SQL shrank while their callback and recursive
query structure remained. No runtime timing was collected. See the
[constructed callback JSON record](../../optimizations/031-postgresql-constructed-callback-json.md)
for controls and limits.

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

Inside these subqueries, eligible scalar predicates can also stay direct when
the callback uses direct native scalar fields and literals combined with
Boolean `AND`/`OR`/`NOT`. For example,
`items:any(item=>item.price>100 item.active)` removes four inner
`CROSS JOIN LATERAL` projections; two outer generic projections remain.
Nullable or optional struct item state and noneligible callback expressions
retain general lowering. This is a SQL-shape observation, not a runtime-speed
claim. See the [relation callback predicate record](../../optimizations/044-postgresql-relation-callback-predicates.md)
for scope and limits.

For a plain bound relation, `has(0)` and `has(-1)` can use this witness query
to test nonemptiness directly. The existing parent and relation presence/null
case remains around the `EXISTS`; no unique item ordering is needed for these
constant endpoints. Other indexes, including `has(-2)`, and invalid Float
indexes retain the cardinality/general path. In the checked empty-table plan,
an indexed child scan appeared beneath a hashed SubPlan and no aggregate or
`COUNT` appeared. This is plan-shape evidence, not a claim about per-row early
termination or runtime speed. See the
[relation endpoint existence record](../../optimizations/043-postgresql-relation-has-existence.md)
for the reproduction and limits.

### Average eligible relation projections directly

For a bound relation with a statically numeric projection, PostgreSQL can
compute a correlated `AVG(numeric)` without materializing the related values
as JSON. Int values enter as exact `numeric`; Float values pass through
round-trip-safe Float text before numeric aggregation and a checked Float
conversion. Null and guarded no-value contributions are skipped, and the
relation's configured presence/null state remains authoritative. This path
does not require relation ordering. Guarded or computed relation receivers
and projections whose numeric family is dynamic or otherwise unrecoverable
retain the general lowering. An outer-column capture in a supported numeric
projection remains correlated to the current row.

The checked fixture reduced generated `WHERE` sizes for five relation
averages: 2,710 to 487 bytes, 2,900 to 495, 3,027 to 865, 2,832 to 695, and
2,259 to 568. The first four queries reduced bound arguments from four to one;
the outer-threshold projection reduced three to one. A plain `EXPLAIN` on an
empty temporary fixture changed the representative physical Int projection
from 23 plan nodes to 7, while both plans retained two sequential scans. These
are SQL-shape and plan observations, not timing or execution-cost claims. See
[direct relation averages](../../optimizations/047-postgresql-relation-average.md)
for the tested queries, reproduction commands, and applicability limits.

### Lower direct collection emptiness and cardinality filters

An unguarded, statically typed root JSONB binding without type metadata can
answer empty List/Map/Struct equality or inequality, bare collection truth, and
zero-size comparisons from its physical JSONB value. Nonzero literal
`size()` comparisons use guarded `jsonb_array_length` for a List root. The
shape check keeps SQL NULL and JSON null from matching at the filter boundary.
For a plain relation, empty-list comparison, truthiness, and zero-cardinality
comparison use correlated `EXISTS` or `NOT EXISTS`, a state test, or a constant
result as appropriate. They do not need an order or a full list aggregate.
Nonzero relation thresholds retain the count-based path. Nested JSON paths,
metadata-backed JSONB, and guarded, computed, or dynamic receivers retain
general lowering. See [direct collection predicate checks](../../optimizations/046-postgresql-collection-predicate-checks.md)
for parity scope and supported forms.

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
