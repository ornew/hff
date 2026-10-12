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

### Parse JSON text with `fromjson()`

For a string receiver, `fromjson()` parses one complete JSON document and
returns its JSON-native HFF value. A recoverable dynamic JSON string is also
accepted; a dynamic receiver of another runtime family returns ordinary null.
The root and all nested arrays, objects, and numbers are validated before the
result is exposed. Invalid syntax, duplicate object keys, invalid Unicode
escapes, int64 overflow, and non-finite or overflowing float values produce
ordinary null for the entire document. No partial value is returned. JSON
whitespace around the single root value is accepted. Float underflow remains
valid and preserves the sign when it rounds to zero.

For example, bind `payload_text` as a string column and declare that field as
`String` in the engine schema. `fromjson()` returns a dynamic value, so its
parsed fields do not need to be enumerated in the schema:

```hff
payload_text:fromjson.active=true
payload_text:fromjson.items:[list]:any(item=>item.price>10.0)
```

This lowering uses PostgreSQL's `IS JSON ... WITH UNIQUE KEYS` and
`pg_input_is_valid` primitives, so PostgreSQL `fromjson()` support requires
version 16 or later; it was tested against PostgreSQL 18.6. See the
[PostgreSQL 16 JSON functions](https://www.postgresql.org/docs/16/functions-json.html)
and [PostgreSQL 16 input-validation functions](https://www.postgresql.org/docs/16/functions-info.html).
The caller must ensure decoded JSON strings and object keys contain no NUL,
which PostgreSQL text cannot represent. Other invalid JSON documents return
ordinary null. SQL lowering does not enforce the local runtime's
`MaxJSONDepth` or step budgets; database execution limits and input-size
controls remain the caller's responsibility.

### Convert strings with `bool()` and `number()`

`bool()` accepts exactly lowercase `true` or `false`; it is case-sensitive and
does not trim surrounding whitespace. `number()` accepts one strict JSON
number spelling, also without surrounding whitespace. Integer spellings must
fit int64 and return int64; a decimal point or exponent selects float64. Invalid
spellings, integer overflow, float overflow, and non-finite results return
ordinary null. Finite subnormals and underflow to signed zero remain valid, so
`-0.0` keeps its float subtype and sign. A null or inapplicable receiver returns
null, while no-value propagates without invoking the conversion.

These conversions can read string columns or runtime JSON strings:

```hff
enabled_text:bool=true
amount_text:number>10.0
payload.value:number:[float]:exists
```

The final example creates a fresh numeric value from stored text. It does not
inherit JSONB float-loss provenance from the source string. Numeric subtype
recovery for values read from JSONB numbers still follows the schema and
metadata rules above. `number()` requires PostgreSQL 16 or later for
`pg_input_is_valid`; `bool()` adds no PostgreSQL version requirement. The
backend was tested with PostgreSQL 18.6. See the
[PostgreSQL 16 input-validation functions](https://www.postgresql.org/docs/16/functions-info.html).

### Unicode `lower()` and `upper()`

`lower()` and `upper()` use locale-independent Unicode simple mappings. They
do not normalize text or apply multi-code-point case expansions, so other
characters remain unchanged when no simple mapping exists. A null or
non-String dynamic receiver returns ordinary null; no-value propagates. This
applies to bound SQL strings and recoverable runtime JSON strings; the
translator uses the Go Unicode tables reported by the PostgreSQL compatibility
profile rather than PostgreSQL's locale-sensitive casing rules. For example:

```hff
name:lower="alice"
name:upper="ALICE"
payload.name:lower="alice"
```

Values must still satisfy the backend's UTF-8 text constraint and contain no
NUL. A dynamic JSONB callback that materializes a list of `Any` values may
require numeric metadata to recover possible numeric elements even when the
callback applies `lower()` and returns strings. A direct `lower()` call on a
recoverable JSON string does not require that metadata.

### Numeric arithmetic

Schema-known scalar `Float` values support `+`, `-`, `*`, and `/`; mixed
`Int`/`Float` operations promote the integer to float64. Remainder (`%`) remains
integer-only. Arithmetic over JSON `Any` and unions also supports `+`, `-`,
`*`, `/`, `%`, and unary negation when the runtime JSON numbers can be
classified and represented. For example:

```hff
price*1.08>=100.0
(payload.amount+tax):[float]:exists
payload.amount%2=0
-payload.amount<0
```

The dynamic form preserves an integer result for integer-only arithmetic and
uses float64 for division or mixed numeric operands. A nonnumeric runtime value
produces ordinary null. Integer overflow, division by zero, float overflow,
and other non-finite float results also produce null. The right operand's HFF
result follows ordinary left-to-right null/no-value propagation; SQL does not
promise physical expression evaluation order or local step budgets.

Float arithmetic follows IEEE 754 round-to-nearest, ties-to-even. Subnormal
results and underflow to signed zero are retained. PostgreSQL's native float
operators are not used as the final rounding step: operands are represented as
exact decimal values, the operation is rounded back to binary64, and division
uses a high-precision decimal candidate with an exact midpoint check. This
preserves HFF results at ties, subnormal boundaries, signed zero, and overflow.
See [design record 015](../../design/015-postgresql-float-arithmetic.md) for
the numerical rationale and tested boundaries.

### Numeric rounding methods

`round()`, `ceil()`, and `floor()` are supported for schema-known `Int` and
`Float` values and for JSON `Any` or unions when the runtime numeric subtype is
recoverable. Integer receivers keep their int64 value and subtype. Float
receivers keep float64, including signed zero; `round()` rounds halfway cases
away from zero (`2.5` to `3.0`, `-2.5` to `-3.0`), while `ceil()` and `floor()`
round toward positive and negative infinity respectively. Null or
non-numeric runtime receivers produce ordinary null, and no-value propagates.

```hff
amount:round=3.0
amount:ceil=4.0
amount:floor=-3.0
payload.value:number:round=3.0
```

For JSONB, metadata is required when storage erased whether an ambiguous value
was `Int` or `Float`; a declared numeric type resolves that subtype choice.
Schema does not restore representation details that storage erased, such as a
negative-zero sign when later serialization or another operation observes it.

Native JSON retains numeric spelling that can distinguish integer and float
values for dynamic operations. JSONB may erase that distinction; provide the
numeric metadata described below when a dynamic operation needs it. Metadata
also preserves a value such as negative zero when the operation or a later
serialization can observe its sign. Schema-known operations only require
metadata for distinctions the stored representation has lost and the
operation needs to recover. Recovered JSON-native values and supported
arithmetic results can be serialized through `json()` or `string()` when their
numeric representation is available; see the
[canonical serialization design](../../design/016-postgresql-json-serialization.md).

Float decoding checks the decimal spelling before the double-precision cast. A
finite spelling below the binary64 range, such as
`1e-400` or `-1e-9999`, becomes positive or negative zero rather than a decode
error; dynamic truth checks treat that decoded zero as false. JSONB can discard
the sign of zero, so use metadata when a later operation must observe that
sign.

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

### Dynamic `size()`, `has()`, and `keys()`

When the receiver is `Any` or a union that admits the relevant object/list
signatures, these methods dispatch by the stored JSON shape:

```hff
data:size=2
data:has("team")
data:has(index)
data:keys=["environment","team"]
```

`size()` returns the number of object members or list elements. `keys()`
requires an object and returns a `List<String>` sorted by Unicode code point.
The receiver selects `has()`'s signature: on an object it accepts a string
key, and on a list it accepts a numeric index. Object lookup tests key
presence even when the stored value is JSON null. List lookup accepts
integer-valued indexes within the signed int64 range, with negative indexes
counting from the end. An int64 index outside the list bounds returns false;
a fractional or out-of-int64 float index returns null.

A null receiver or one with no applicable object/list family produces null
before argument evaluation, so a later no-value argument is skipped. Once the
receiver selects a family, a reached no-value argument propagates. A runtime
argument of the wrong family produces null. `size()` and `keys()` do not need
numeric metadata, and dynamic object `has()` with a string key does not either.
For list `has()`, a statically typed integer or float index works with plain
JSONB. A dynamic numeric argument requires native JSON or JSONB metadata when
the stored number could be an integer or float: a valid but out-of-range
integer index is false, while an invalid float index is null, so collapsing
the numeric representation would change the result.

### Dynamic object `get()`, `values()`, and `items()`

An `Any` or union receiver can use these methods when its schema admits an
object shape. `get(key)` accepts a string key; `values()` and `items()` enumerate
the object's actual keys in Unicode code-point order, matching `keys()`. The
values and key/value pairs use that same order:

```hff
data:get("team")="platform"
data:values=[1,2]
data:items=[["a",1],["b",2]]
```

The receiver is checked first. A null or inapplicable receiver returns
ordinary null without evaluating the key. For an applicable object receiver,
a reached no-value key propagates; an ordinary null or a key with the wrong
runtime type produces ordinary null. `get()` returns null for both a missing
key and a key whose stored value is JSON null; use `has()` to distinguish
those cases. A null key does not require numeric metadata.

Values selected or enumerated from a schema-known object are normalized using
their declared field types before they are returned or aggregated. This keeps,
for example, a JSONB field declared as float distinct from an integer even
when its stored numeric spelling is `1`. With a constant key such as
`data:get("price")`, only the selected field's type needs to be recoverable;
unrelated fields do not require numeric metadata. A dynamic key can select any
member, so its candidate field types must be recoverable wherever JSONB has
erased a needed numeric distinction. Native JSON retains JSON-native runtime
types for `Any`; JSONB `Any` requires metadata when a selected or enumerated
numeric subtype must be restored. Metadata restores numeric representations
only; it does not add support for bytes, timestamps, or durations inside
dynamic JSON values. Plans whose selected result cannot be determined from the
schema and available representation information remain Build errors.

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
promise PostgreSQL's physical callback evaluation order.

### Dynamic JSON callbacks

An `Any` or union receiver can use `any()`, `all()`, `map()`, `flatmap()`,
`foldl()`, and `foldr()` when its schema admits a list shape. The backend checks
the runtime JSON shape before invoking callbacks: a no-value receiver
propagates, while a null or non-list receiver returns ordinary null. An empty
list produces false for `any()` and true for `all()`. Callback results for
`any()` and `all()` use the usual HFF boolean conversion, including guarded
no-value as false.

```hff
data:any(x=>x:exists)
data:all(x=>x=true)
data:map(x=>"fixed")=["fixed","fixed"]
data:flatmap(x=>["fixed"])=["fixed","fixed"]
data:foldl(0,(acc,x)=>acc+1)=2
```

`map()` retains callback results in input order, including ordinary nulls;
guarded no-value propagates to the whole method. `flatmap()` concatenates only
list callback results, preserving outer and inner ordinal order. A null or
non-list callback result makes the whole result ordinary null, while a reached
guarded no-value propagates as no-value. The first callback result that
determines a whole-method failure controls its result. These are HFF result
semantics; PostgreSQL's physical callback evaluation order and local
`MaxSteps`/`MaxGeneratedValues` budgets are not guaranteed.

Callback outputs are normalized against their inferred schema types before
being retained or aggregated. A constant string result such as `map(x=>"fixed")`
does not need numeric metadata. An operation that observes a numeric
distinction JSONB has erased, such as an integer-versus-float type guard, or
retains an ambiguous numeric value, requires native JSON or sufficient JSONB
metadata. Truthiness and `exists()` do not inherently need numeric subtype
metadata. `flatmap()` restores the callback's list candidates; an irrelevant
scalar integer/float alternative does not by itself require metadata. As
elsewhere, unsupported execution-budget requirements are rejected by `Build`.

`foldl()` calls its callback as `(accumulator, item)` in list ordinal order;
`foldr()` calls it as `(item, accumulator)` in reverse ordinal order. The
accumulator type is inferred from the initial value and all callback results,
including `Any` and scalar/list/object unions of JSON-native values. The backend
normalizes the seed and each callback result before using it as the next
accumulator. An empty list returns the normalized initial value, preserving
its type; an ordinary
null callback result becomes the next accumulator, while no-value stops the
fold and propagates. A null or non-list receiver returns ordinary null before
the seed or callback is evaluated.

A callback that ignores an ambiguous JSONB item and returns a known type, such
as `foldl(0,(acc,item)=>acc+1)`, does not need metadata for that item's numeric
subtype. When a callback retains an `Any` or ambiguous value in the accumulator,
JSONB needs metadata if its storage erased a numeric distinction the result
depends on; normalization itself preserves the recovered value. Native JSON
retains JSON-native types. Type guards such as `item!` and access through a
constructor, `get()`, field, or index preserve recovered types. JSON
constructors inside a callback also normalize their represented children
before the result is retained, so a list or object literal does not hide a lost
JSONB subtype.
PostgreSQL does not provide local `MaxSteps` or `MaxGeneratedValues` enforcement
or a guarantee about physical callback evaluation order. JSONB metadata and
these dynamic folds do not add support for bytes, timestamps, or durations.

```hff
profile.scores:foldl(0,(acc,score)=>acc-score)=-170
profile.scores:foldr(0,(score,acc)=>score-acc)=-10
```

These are HFF result-order guarantees. SQL does not promise physical callback
evaluation order or local `MaxSteps`/`MaxGeneratedValues` enforcement; unsupported
explicit execution-budget requirements are rejected by `Build`.

For schema-known scalar columns and JSON/JSONB values, `json()` and
`string()` support null, bool, int64, string, and finite float64 values.
Recovered JSON-native `Any` and union values are also supported, including
recursive lists, maps, and structs whose numeric kinds can be determined from
the schema, native JSON spelling, or JSONB metadata. `json()` emits compact
JSON; `string()` leaves a root string unquoted and uses compact JSON for
collections. Object keys are sorted with PostgreSQL's `C` collation, arrays
retain element order, and stored nulls remain JSON nulls. A null receiver
returns the text `"null"`; no-value propagates. For example:

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

For dynamic JSON values, the backend first resolves recoverable schema and
numeric metadata, then serializes the normalized JSON-native result. This
preserves integer-versus-float kinds inside unions and nested collections,
including arithmetic results. The SQL serializer does not promise enforcement
of the local runtime's `MaxJSONDepth`; database execution limits and
service-level input bounds remain the caller's responsibility. See the
[serialization design](../../design/016-postgresql-json-serialization.md) for
the canonical lowering and its scope.

JSONB can discard integer-versus-float spelling and the sign of zero. Without
metadata, operations requiring those distinctions remain rejected, including
when a callback returns a stored JSONB float. With supported numeric metadata,
the backend restores the recorded numeric subtype and value before typed
lowering. Native JSON continues to use its stored numeric spelling. Dynamic
representations still require schema or metadata information sufficient for
the particular operation; `Any` is not a general escape from type recovery.
Current metadata supports numeric types only: bytes, timestamps, and durations
are not restored by type tags. Dynamic serialization is limited to JSON-native
values whose numeric representation is recoverable; it does not make arbitrary
`Any` values serializable.

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
which kind was stored. If the SQL backend cannot faithfully lower the declared
scalar comparison, `Build` returns a source-located `hff.BackendError`;
statically incompatible query types are rejected by compilation.

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
must provide metadata when an operation observes representation details that
JSONB loses, such as the sign of zero during serialization. Ordinary numeric
comparisons treat positive and negative zero as equal. A non-null value with
an incompatible runtime type yields ordinary HFF null. You can inspect that
result with `type()`:

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
usual HFF predicate conversion. Two JSON-backed dynamic values can also be
compared when both representations are recoverable. Scalar values use the
ordinary HFF comparison rules. Lists and objects use recursive structural
equality for `=` and `!=`: lists compare length and position, objects compare
their actual keys and values, and numeric values compare without rounding an
int64 through float64. JSONB needs numeric metadata wherever normalization
would lose a value or its HFF numeric kind; schema-known numeric candidates can
remove that need when they determine the kind. The sign of zero does not alter
ordinary numeric comparison results; its metadata text matters to operations
that observe the exact representation, such as serialization.

An incompatible runtime family at the comparison root produces ordinary HFF
null, which matches no row as a predicate. A runtime-null or no-value operand
compares false. Within a list or object comparison, a mismatch between nested
value families makes the structures unequal; a stored null equals a stored
null at the same location, while a missing object key differs from a present
null key. This preserves the distinction between a top-level dynamic type
mismatch and a recursive structural mismatch.

Dynamic-to-dynamic ordering is available for compatible strings and numbers;
booleans support equality and inequality, while lists and objects support
structural equality and inequality. The same lowering can compare a dynamic
JSON value with a statically represented JSON collection or literal. For
JSONB, numeric values in either operand must be recoverable from the declared
schema or numeric metadata. Method calls on an `Any` or union receiver remain
available only when the schema and stored representation resolve that
operation's behavior. Comparing a dynamic value with an unsupported scalar
counterpart is still rejected by `Build`.

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
argument produces null when the receiver is a string. `contains()` also has
dynamic String/List dispatch, described below. Dynamic `concat()` is described
in the next section.

### `contains()` on dynamic strings and lists

When the schema permits a string or list receiver, the backend dispatches
`contains()` to the matching HFF signature. A schema such as
`String|List<String>` supports both of these runtime JSON values without
numeric type metadata:

```hff
labels:contains("platform")
```

For a string receiver, every supplied substring must occur in the string. The
selected String signature requires each argument to evaluate to a string. A
runtime argument of another type yields ordinary null when reached and skips
remaining arguments. A null string argument propagates null; a no-value
receiver or reached argument propagates no-value. For a list receiver, every
supplied needle must be present using HFF structural equality. A null needle
can match a stored null element when the declared element type permits null; a
no-value needle propagates no-value.

The receiver is evaluated first. If it is null or no-value, or a dynamic
receiver has no applicable String/List family, the result is determined
before its arguments are evaluated. Otherwise arguments are evaluated from
left to right before containment is checked: a missing substring or element
does not skip a later argument. If evaluating a reached argument produces
null or no-value, that state propagates and later arguments are not evaluated.
A null list needle is data rather than propagated null, so argument evaluation
continues. Thus a JSON number or object used as
`labels` yields ordinary null for the example predicate, while a null
`labels` value also produces a non-match through ordinary predicate
conversion.

That number/object behavior applies to an `Any` receiver. With a declared
`String|List<String>` receiver, an incompatible host value is rejected by
schema/input validation rather than dynamically treated as an `Any` value.

The schema determines which numeric kinds can occur in list values. A
`List<String>` candidate needs no numeric metadata. If a dynamic list may
contain numbers, JSONB requires metadata when normalization erases a numeric
kind or value needed for structural membership. Local evaluation still
requires the corresponding list/object equality capability when composite
elements may be compared.

### `concat()` on dynamic strings and lists

`concat()` dispatches to the matching String or List signature when the
receiver is `Any` or a union with a supported candidate. String concatenation
joins the two strings. List concatenation preserves the left operand's items,
then the right operand's items, including order and duplicates. It normalizes
list operands before concatenation so later explicit type guards such as
`:[int]` and `:[float]` retain known element types. For example, concatenating
schema-known `List<Float>` and `List<Int>` values preserves the float type for
the left element even if JSONB stored its number as `1`:

```hff
data:concat("!")="Alice!"
data:concat(["platform"]):contains("platform")
(left:concat(right))[0]:[float]:exists
```

For a dynamic `Any` receiver, a runtime value that does not match the selected
String or List family yields ordinary null. A null receiver or an inapplicable
receiver determines the result before argument evaluation, so a later
no-value argument is skipped. Once the receiver selects a signature, null
arguments propagate null; a reached guard that produces no-value propagates
no-value. Unlike list `contains()`, a null `concat()` argument is not a value
to append.

Native JSON supports these operations on `Any` when the stored JSON-native
representation identifies the applicable signature. Plain JSONB supports
schema-known string and list candidates whose element types are recoverable.
Bytes, timestamps, and durations in `Any` are not supported. An `Any` list or
an ambiguous numeric list requires JSONB type metadata when JSONB storage has
already erased distinctions needed by concatenation or a later operation;
concatenation's normalization does not itself lose those distinctions. The
schema must also admit the resulting value for chained operations; unsupported
representations are rejected during `Build`.

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
`in()` and `unique()` also compare `Any` and union elements recursively when
their runtime representation can be recovered. JSONB metadata is needed when
an operation must distinguish numeric kinds or values that JSONB normalization
has erased; string-only membership over dynamic JSON does not need numeric
metadata.

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
(payload.amount+tax):json="2.5"
data:[list]:map(item=>item):json="[1,1.0]"
```

The last two queries compare serialized dynamic arithmetic and callback
results. For JSONB, provide metadata when storage erased a numeric distinction
needed to determine the output's integer or float kind.

The following operations remain unsupported for JSON-backed values and return
a source-located `hff.BackendError` from `Build`:

- Dynamic comparisons for which the schema, JSON spelling, and configured
  metadata cannot recover the operand representations. Dynamic `type()` and
  `exists()` remain available. Comparisons with a statically typed scalar,
  another recoverable dynamic value, or a statically represented JSON
  collection are supported as described above. JSONB needs numeric metadata
  when both integer and float candidates remain possible or an operation needs
  a numeric distinction lost during JSONB normalization.
- Dynamic string methods other than `len()`, `lower()`, `upper()`, `bool()`,
  `number()`, `fromjson()`, `startswith()`, `endswith()`,
  String/List `contains()`, and String/List `concat()` described above.
- Other dynamic operations whose result cannot be resolved from schema
  candidates, a supported type guard, or a statically typed string/numeric
  index.
- Dynamic folds whose seed or callback-result representations cannot be
  determined from the schema and available JSONB metadata.
- Conversions to bytes/timestamps/durations, float64
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
semantics. Comparisons between two dynamic values, or between a dynamic value
and a statically represented JSON collection, are supported when both operand
representations are recoverable as described above. Comparisons with one
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
