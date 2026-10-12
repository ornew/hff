# PostgreSQL JSON and JSONB Bindings

The PostgreSQL backend can bind a schema-known HFF value to one native JSON or
JSONB column. A JSONB binding can also name a separate version-1 JSON Pointer
type-metadata column to restore numeric distinctions that JSONB normalizes.
`Build` lowers field access, list indexing, selected collection
inspection, structural equality, predicates, and type guards into a
parameterized predicate. It does not connect to PostgreSQL, inspect the table
schema, or validate stored documents.

## Bind one JSON document

Declare the JSON document's HFF shape in the engine schema. Schema-known
operations use the declared type. Native JSON preserves numeric spelling used
by dynamic type guards; JSONB needs the metadata described below only for
numeric distinctions it loses. Guards and metadata do not make every dynamic
operation available.

For example, the HFF path `profile` can be stored as one physical JSONB
column. These rows have the database shape used below:

```sql
CREATE TABLE records (
    id bigint PRIMARY KEY,
    profile jsonb NOT NULL
);
INSERT INTO records VALUES
    (1, '{"name":"Ada","scores":[80,90]}'::jsonb),
    (2, '{"name":null,"scores":[]}'::jsonb);
```

The first JSON document is reconstructed as the HFF value
`{"profile":{"name":"Ada","scores":[80,90]}}`. The database column is
bound at the root HFF path `profile`; `profile.name` and `profile.scores` are
then fields inside that same document, not separate column bindings.

```go
profileType := schema.Struct(
    schema.Nullable(schema.String("name")),
    schema.List("scores", schema.IntType),
)
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.Field("profile", profileType),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `profile.name="Ada" profile.scores:has(-1)`)
if err != nil {
    return err
}

query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindJSON(
        hff.Path{"profile"},
        hffsql.Column("records", "profile"),
        hffsql.JSONB,
    )),
)
if err != nil {
    return err
}
rows, err := db.QueryContext(ctx,
    `SELECT records.id FROM records WHERE `+query.Where(), query.Args()...)
```

Use `hffsql.JSON` or `hffsql.JSONB` to state the physical PostgreSQL column
type. Ordinary scalar bindings continue to use `Bind`; JSON bindings use
`BindJSON(path, column, format)`. The path identifies the complete schema
value stored in that column. Its nested struct fields and list/map contents
are accessed inside the JSON document. Do not bind both a JSON ancestor and
one of its descendant paths, and do not map two schema paths to one physical
column using conflicting formats; `Build` rejects these conflicting
mappings. `Column("records", "profile")` names two separately quoted SQL
identifier components; it is not raw SQL text or an HFF path.

The query values remain bound parameters. `Where()` and `Args()` are the
database-facing artifact; the application owns execution, connections,
transactions, and timeouts. See the [PostgreSQL backend guide](README.md) for
the general construction contract.

## Supported profile

For statically known values, the schema determines the HFF representation.
Supported families are null, bool, int64, finite float64, string, homogeneous
lists, string-keyed maps, and structs; nullable variants are supported. JSONB
metadata can restore numeric subtype and value when normalization would lose
the distinction. The application must store data and metadata that satisfy
the declared schema and remain consistent with each other. `Build` does not
inspect rows, read the database, or validate stored documents or metadata.

Known-schema field access is supported for nested struct/map fields. List
indexing accepts negative indexes from the end and integer-valued floating
indexes. A fractional or out-of-range index produces a null result. Missing
object keys can be checked with `has()`; `get()` returns null for a missing
key, so use `has()` when that distinction matters. JSON null remains a value
and propagates through the ordinary HFF evaluation rules.

For schema-known JSON objects, the backend supports `size()`, `has(key)`,
`get(key)`, `keys()`, `values()`, and `items()`. Enumeration orders keys with
PostgreSQL's `C` collation. For schema-known JSON lists, it supports `size()`,
`has(index)`, `contains()`, `unique()`, `sort()`, `concat(list)`, `any()`,
`all()`, `map()`, and `flatmap()`. `contains()` requires every supplied needle
to be present; a null
needle is an ordinary value, while a null list receiver propagates null. A
no-value argument propagates even if an earlier needle matched. `unique()`
compares each element
by HFF equality, so repeated stored nulls and repeated nested composite values
are duplicates. These operations require applicable list/object equality
capabilities for the types actually compared.

`sort()` returns a stable ascending copy. Schema-known integer and float lists
sort numerically; strings sort by Unicode code point using PostgreSQL's `C`
collation. Equal values retain their input order. A runtime list containing
null or incompatible/non-orderable values produces null. A statically known
type with no orderable member is rejected during compilation. A
JSON-distinguishable union can be sorted when all runtime elements share one
orderable family; an ambiguous int/float union remains unsupported without
type metadata.

`concat(list)` preserves left-to-right order, duplicates, and null elements.
JSON- or JSONB-backed lists can be concatenated with schema-compatible list
values, including a list from the other physical format, or query-side list
literals. A null argument produces a typed JSON null, so subsequent indexing or
list methods propagate null; no-value propagates by the ordinary HFF rules.

`map()` preserves input order and retains null callback results as elements. A
guarded no-value result propagates to the whole method. `flatmap()` appends
each callback list in input order and preserves each inner list's order. A null
or non-list callback result produces null for the whole method; guarded
no-value propagates as no-value. Evaluation stops at the first callback result
that determines a whole-method result, so a later guard cannot override an
earlier null or no-value result. This is HFF result precedence; it does not
promise PostgreSQL's physical callback evaluation order. Callback result types
must be determined by the schema; dynamic `Any` and ambiguous int/float unions
remain Build errors.

`foldl()` and `foldr()` use the same known-type boundary for accumulators:
int64, bool, string, or schema-known lists. The accumulator type includes the
initial value and every callback result, and the callback must type-check for
that inferred type. Mixed distinct scalar accumulator types, dynamic `Any`, and
ambiguous int/float unions remain Build errors. Folds visit list elements in
ordinal order (`foldl`) or reverse ordinal order (`foldr`). An empty list returns
the initial value, including null; a null receiver returns null. A null callback
result becomes the next accumulator, while guarded no-value propagates to the
whole fold.

```hff
profile.scores:foldl(0,(acc,score)=>acc-score)=-170
profile.scores:foldr(0,(score,acc)=>score-acc)=-10
```

These are HFF result-order guarantees. SQL does not promise physical callback
evaluation order or local `MaxSteps`/`MaxGeneratedValues` enforcement; unsupported
explicit execution-budget requirements are rejected by `Build`.

For schema-known scalar columns and JSON/JSONB values, `json()` and
`string()` support null, bool, int64, string, and finite float64 values,
including recursive lists, maps, and structs containing those types when their
numeric representation is available. `json()` emits compact JSON;
`string()` leaves strings unquoted and uses compact JSON for collections.
Object keys are sorted with PostgreSQL's `C` collation, arrays retain element
order, and stored nulls remain JSON nulls. A null receiver returns the text
`"null"`; no-value propagates. For example:

```hff
profile.scores:json="[80,90]"
profile.name:string="Ada"
```

Float serialization uses a canonical shortest round-tripping decimal with a
float marker, canonical exponent form, and preserved negative zero. Its output
does not depend on PostgreSQL's `extra_float_digits` setting. The formatter
reconstructs the exact IEEE value and searches decimal precisions from one to
seventeen using nearest, ties-to-even rounding; this correctness-first SQL
lowering is a baseline, not a performance guarantee.

JSONB can discard integer-versus-float spelling and the sign of zero. Without
metadata, operations requiring those distinctions remain rejected, including
when a callback returns a stored JSONB float. With supported numeric metadata,
the backend restores the recorded numeric subtype and value before typed
lowering. Native JSON continues to use its stored numeric spelling. Dynamic
representations still require schema or metadata information sufficient for
the particular operation; `Any` is not a general escape from type recovery.
Current metadata supports numeric types only: bytes, timestamps, and durations
are not restored by type tags, and `fromjson()` remains unsupported.

Nested callbacks can capture outer callback values and schema fields, with
lexical shadowing:

```hff
orders:any(order=>order.items:any(item=>item.price>100))
```

Inside those callbacks, scalar membership is supported as well:

```hff
profile.scores:any(score=>score:in(80,90))
```

The SQL backend preserves the callback result rules: a null list yields null,
an empty list yields false for `any()` and true for `all()`, and each callback
result is boolean-converted (including guarded no-value as false). AND, OR, and
NOT keep their normal short-circuit and boolean behavior. This describes HFF
results; SQL does not promise a physical callback evaluation order or enforce
local `MaxSteps`/`MaxGeneratedValues` budgets. Explicit SQL execution-budget
requirements unsupported by the selected backend are rejected by `Build`.

Truthiness, `exists()`, `type()`, scalar comparison, and structural type guards
are available when their types are known from the schema. The backend rejects
an operation during `Build` when the JSON storage type cannot preserve the
required HFF semantics.

### Compare a dynamic JSON value with a scalar

An `Any` value from a JSON or JSONB document can be compared with a scalar
whose HFF type is known from the query. String and numeric comparisons support
the ordering operators; booleans support equality and inequality. Numeric
comparisons use both the value's recoverable representation and the schema's
possible numeric kinds. Native JSON retains whether a number was written as an
integer or float. A schema that permits only one numeric kind, such as
`Int|String` or `Float|String`, also makes JSONB comparisons unambiguous and
does not require numeric metadata. If the schema permits both integer and float
values, such as `Any` or `Int|Float`, JSONB needs type metadata to recover
which kind was stored. Comparing two dynamic values, or using an unsupported
scalar counterpart, is rejected by `Build`.

For example, with `data` declared as `Any` and bound to a JSON document:

```hff
data="Alice"
data<"Bob"
data=true
data=9007199254740993
```

The runtime JSON type and schema's numeric candidates determine whether the
comparison is meaningful. A schema-known integer is decoded from the numeric
value through an exact integer conversion; the schema remains authoritative
even when JSON spells that value as `1.0` or `1e0`. The caller must ensure it
is integral and within the int64 range. JSONB can use this path without subtype
metadata when the schema leaves only the integer candidate. If both integer
and float candidates remain possible, JSONB still needs metadata. The caller
must also provide metadata when JSONB has lost part of the numeric value, such
as the sign of zero. A non-null value with an incompatible runtime type yields
ordinary HFF null. You can inspect that result with `type()`:

```hff
(data="Alice"):type="null"
```

At the top-level predicate boundary, that null result matches no row. A null or
no-value operand is instead consumed as boolean false by the dynamic
comparison. Consequently, when `data` itself is null, this predicate is true:

```hff
(data="Alice"):type="bool"
```

These rules distinguish a type mismatch from a null operand while keeping the
usual HFF predicate conversion. They do not enable comparisons between two
dynamic values. Method calls on an `Any` or union receiver are supported only
when the schema and available representation resolve the operation's behavior.

### Preserve numeric candidates in collection guards

Type guards on schema unions retain the declared element candidates while
checking the actual JSON collection. For example, when the source schema is
`List<Float>|List<Float|null>`, this guard can recognize a float list in
JSONB without numeric type metadata:

```hff
data:[list[float]]:exists
```

Both union members establish that numeric elements are floats; JSONB's loss of
their original numeric spelling does not make this guard ambiguous. A schema
that allows both integer and float elements, such as
`List<Int>|List<Float>`, still needs recoverable numeric subtype information
for JSONB. Native JSON preserves the numeric spelling for that case.
Other operations on a union are supported only when its candidates let the
backend resolve their result; for example, an `Int|String` profile
can be serialized with `json()`. This does not make arbitrary union method
calls available.

### String methods on dynamic JSON values

The backend supports `len()`, `startswith()`, and `endswith()` when a JSON
receiver or one of these methods' string arguments has a dynamic `Any` or
union representation. A runtime string is extracted and passed to the ordinary
string operation; a non-string runtime value becomes ordinary HFF null. This
works both for a dynamic receiver and for a known string receiver with a
dynamic JSON argument:

```hff
data:len=5
data:startswith("Al")
data:endswith("ice")
name:startswith(data)
(data:len):type="null"
```

Here `data` can be an `Any` JSON value and `name` a schema-known string.
When reached during evaluation, a no-value receiver or argument remains
no-value. For methods with an argument, an ordinary null receiver or a
non-string dynamic receiver produces null and suppresses a later no-value
argument under the normal receiver-first evaluation rule. A non-string dynamic
argument produces null when the receiver is a string. These dynamic
receiver/argument rules do not extend `contains()` or `concat()` to arbitrary
JSON values.

### Boolean methods on dynamic JSON values

`not()`, `and()`, and `or()` accept a JSON `Any` or union receiver or argument
when the declared schema identifies a boolean candidate. At evaluation, a JSON
boolean is converted to HFF bool; another JSON type yields ordinary HFF null.
The backend does not apply truthiness conversions to strings, numbers, arrays,
or objects. A known boolean can also be combined with a dynamic JSON argument:

```hff
data:not
data:and(flag)
flag:or(data)
(data:and(flag)):type="null"
```

Here `data` is a dynamic JSON value and `flag` is a schema-known bool. A
non-boolean runtime value produces ordinary null when the method reaches that
receiver or argument. A null or no-value receiver suppresses evaluation of the
argument. Boolean short-circuit rules also apply: `false:and(...)` skips its
argument, and `true:or(...)` skips its argument, including an argument that
would otherwise be no-value. When an operand is reached, its no-value state
propagates normally.

### Structural equality and collection operations

List equality compares elements in order and requires equal lengths. Object
equality compares the actual key/value entries, including for structs and
string-keyed maps; a missing field differs from a present field containing
null. A stored null compares equal to null in the same position. Nested lists
and schema-known heterogeneous JSON values are compared recursively, including
known int64 and float64 values without converting int64 through float64.
Structural comparisons in `=`, `!=`, membership, and `unique()` require the
matching `comparison:list-equality` or
`comparison:object-equality` capability (or the combined
`group:structural-equality`). Explicit null tests do not require either one.
At the top-level predicate boundary, `=` and `!=` both return false when the
comparison result is null or no-value, following HFF predicate conversion.

For example, list membership, uniqueness, and a callback using scalar `in()`
can be combined with structural equality:

```hff
profile.scores=[80,90]
profile.scores:contains(80,90)
profile.scores:unique
profile.scores:sort=[80,90]
profile.scores:concat([100])=[80,90,100]
profile.scores:any(score=>score:in(80,90))
profile.scores:map(score=>score+1)=[81,91]
profile.scores:flatmap(score=>[score,score])=[80,80,90,90]
```

The following operations remain unsupported for JSON-backed values and return
a source-located `hff.BackendError` from `Build`:

- Comparisons between two dynamic values and method calls on an `Any` or union
  receiver when the schema and stored representation cannot resolve their
  semantics. Dynamic `type()` and `exists()` remain available. A dynamic
  value compared with one statically typed scalar is supported as described
  above; JSONB needs numeric metadata when both integer and float candidates
  remain possible or when part of the numeric value, such as negative zero,
  has been lost.
- Dynamic string methods other than `len()`, `startswith()`, and `endswith()`;
  `contains()` and `concat()` are not dynamically overloaded.
- Other dynamic operations whose result cannot be resolved from schema
  candidates, a supported type guard, or a statically typed string/numeric
  index.
- Other unsupported collection transformations.
- `fromjson()`, conversions to bytes/timestamps/durations, float64
  serialization when JSONB has lost required numeric representation, and
  operations requiring a runtime type not fixed by the schema or metadata.

Unsupported behavior is rejected rather than delegated to local evaluation.
The [method reference](../../reference/methods.md) lists built-in signatures;
the [normative specification](../../../spec/methods.md) defines their
language-level semantics.

## Storage requirements and limits

The caller must keep the PostgreSQL column type and stored document values
compatible with the declared schema, including numeric subtype and nullability.
Documents must not contain duplicate object keys. Strings must be valid UTF-8
without NUL; int64 values must be in range, and float64 values must be finite.
`Build` does not introspect database declarations or rows; malformed or
schema-incompatible stored values can fail during database evaluation.

For schema-known integer values, a JSON number is interpreted as int64
according to the schema even if its spelling has a decimal point or exponent,
such as `1.0` or `1e0`. The stored value must be an exact integer within the
int64 range; fractional and out-of-range numbers violate the caller's storage
contract. The backend does not validate that contract in advance. Schema-known
floats are decoded as float64. JSONB can lose the sign of zero, so provide
numeric metadata when negative zero must be preserved.

JSONB normalizes numeric spelling, so a JSONB data column may be paired with a
metadata column using `BindJSON(path, dataColumn, JSONB,
WithTypeMetadata(metadataColumn))`. The version-1 sidecar has this shape:

```json
{
  "version": 1,
  "types": {
    "/price": {"type": "float", "text": "-0.0"},
    "/items/0/value": {"type": "int"},
    "/a~1b~0": {"type": "float"}
  }
}
```

`types` maps JSON Pointer paths in the data document to numeric HFF types.
Pointer segments escape `~` as `~0` and `/` as `~1`. The empty pointer `""`
addresses the bound document root; `/` addresses an object's empty-string key.
Array indexes are zero-based decimal segments. The
`text` member is optional and is needed only when the JSONB number alone cannot
reconstruct the HFF numeric value and kind, such as negative zero. It is a
valid HFF numeric spelling, not a promise to preserve the original lexical
exponent or formatting. Integer and float markers preserve the HFF numeric
kind.

For example, bind the data document and its sidecar together:

```go
hffsql.BindJSON(
    hff.Path{"payload"},
    hffsql.Column("records", "payload"),
    hffsql.JSONB,
    hffsql.WithTypeMetadata(hffsql.Column("records", "payload_types")),
)
```

For example, this JSONB data value and sidecar preserve the float type and
negative zero at the `balance` key:

```sql
CREATE TABLE records (
    id bigint PRIMARY KEY,
    payload jsonb NOT NULL,
    payload_types jsonb NOT NULL
);
INSERT INTO records VALUES (
    1,
    '{"balance":0}'::jsonb,
    '{"version":1,"types":{"/balance":{"type":"float","text":"-0.0"}}}'::jsonb
);
```

The corresponding HFF expression can inspect the value with a known type:

```hff
payload:[map[float]]["balance"]:string="-0.0"
```

Bind both database columns to the same HFF path using the JSONB binding call
above. The sidecar is stored alongside the document; it is not a second HFF
field.

The caller must keep the sidecar's version and entries consistent with the data
document and the declared schema. `Build` checks that the binding is configured
for JSONB and rejects unsupported or unrecoverable plans, but performs no
database I/O, row inspection, or runtime full-document metadata validation.
Missing or inconsistent stored metadata violates the caller's storage
contract; it is not detected by a runtime full-document validation pass.
Type metadata currently restores numeric representations only. Metadata tags
for bytes, timestamps, or durations are unsupported. Schema-known JSON-native
kinds such as booleans, strings, arrays, objects, and null continue to use their
JSON representation. Metadata-backed dynamic type guards and statically typed
string or numeric indexing are supported for the verified profile. Dynamic
`type()` and `exists()` are available directly. Other `Any`/union operations
require schema candidates and the stored representation to resolve their
semantics; comparisons between two dynamic values remain unsupported even
when numeric metadata is present. Comparisons between one dynamic value and one
statically typed scalar follow the rules above.
Unsupported ambiguous HFF unions or operations are rejected rather than
guessed.

For a fixed unguarded field chain rooted at a variable whose nearest binding is
a direct metadata-backed JSONB binding, the translator can restore numeric
metadata from only the selected subtree. The lookup still uses the subtree's
JSON Pointer within the original document; it does not rewrite the caller's
sidecar contract. Whole-document access and paths whose resolution depends on
a guard, computed receiver, dynamic index, callback parameter, or nested
physical binding keep the general restoration path. A fixed field prefix that
can be extracted before a later guard remains eligible. Within a callback, an
outer `doc.price` path can qualify; a path rooted at the callback item does
not. See the
[PostgreSQL translation choices](optimizations.md#restore-only-a-selected-jsonb-metadata-subtree)
for the scope of this SQL-shape optimization.

PostgreSQL is the only supported SQL dialect. JSON and JSONB use their native
operators; this support does not promise stable generated SQL text or database
execution order. SQL execution constraints are reported by the SQL API, and
the service remains responsible for database timeouts and cancellation.

## Verification

The repository's tagged integration suite compares JSON and JSONB bindings
against local evaluation on PostgreSQL 18.6. The verified matrix covers 5,556
JSON/JSONB and 5,680 scalar query-record pairs, plus 152 extension cases. It
covers scalar and nested access, null and missing-key cases, list indexing,
object/list inspection and equality, callbacks, canonical serialization,
type guards, and type/truthiness predicates. Float serialization tests include
1,024 deterministic IEEE samples and run with `extra_float_digits=-3`.
To run it against a PostgreSQL
container with `psql` installed:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -tags=integration ./backends/sql
```

See [development checks](../../development/checks.md) for the test environment.
Run the [JSON binding example](https://github.com/ornew/hff/tree/main/examples/sql-json)
and [JSONB numeric-metadata example](https://github.com/ornew/hff/tree/main/examples/sql-json-metadata)
from the repository root:

```sh
go run ./examples/sql-json
go run ./examples/sql-json-metadata
```
