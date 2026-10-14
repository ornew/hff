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
Supported families are null, bool, int64, finite float64, string, Bytes,
Duration, Timestamp, homogeneous lists, string-keyed maps, and structs;
nullable variants are supported. JSON represents schema-known Bytes as
standard padded base64 strings, Durations as canonical duration strings, and
Timestamps as canonical UTC RFC 3339 strings. JSONB preserves those strings,
so they need no numeric type metadata. JSONB metadata can restore numeric
subtype and value when normalization would lose the distinction. The
application must store data and metadata that satisfy the declared schema and
remain consistent with each other. `Build` does not inspect rows, read the
database, or validate stored documents or metadata.

### Duration values in JSON documents

When the schema declares a field as Duration, JSON and JSONB store it as a
canonical HFF duration string. For example, `1m30s` is a Duration under a
Duration schema, while the same JSON string under `Any` remains a String.
JSONB does not normalize string contents, so Duration decoding requires no
numeric type metadata. This support covers a schema-known Duration or nullable
Duration field, including typed nested fields and collection elements. For the
schema-known unions `Duration|Int|Null`, `Duration|Bool|Null`,
`Duration|Int|Bool|Null`, and `Duration|Float|Null`, supported operations
dispatch by JSON category: strings select Duration, booleans select Bool, and
numbers select the declared numeric alternative. JSON null remains null. These
category distinctions work for both JSON and JSONB without Duration metadata.

For example, a document can hold each alternative in the same column:

```sql
CREATE TABLE duration_union (
    id bigint PRIMARY KEY,
    value jsonb NOT NULL,
    items jsonb NOT NULL
);
INSERT INTO duration_union VALUES
    (1, '"0.000000001s"'::jsonb, '["0.000000001s",7,true,null]'::jsonb),
    (2, '7'::jsonb, '[7,true,null]'::jsonb),
    (3, 'true'::jsonb, '[false,"0s"]'::jsonb),
    (4, 'null'::jsonb, '[]'::jsonb);
```

Declare the union at the bound path and bind the JSON column with that schema:

```go
valueType := schema.Union(schema.DurationType, schema.IntType, schema.BoolType, schema.NullType)
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.Field("value", valueType),
        schema.List("items", valueType),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}
program, err := e.Compile(ctx, `value:type="duration"`)
if err != nil {
    return err
}
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindJSON(
        hff.Path{"value"}, hffsql.Column("duration_union", "value"), hffsql.JSONB,
    ), hffsql.BindJSON(
        hff.Path{"items"}, hffsql.Column("duration_union", "items"), hffsql.JSONB,
    )),
)
if err != nil {
    return err
}
```

The following forms are supported against that declared schema:

| Operation | HFF expression |
| --- | --- |
| `type()` | `value:type="duration"` |
| Truthiness | `value` |
| Duration unit | `value:seconds>0` |
| Explicit Duration guard | `value:[duration]:nanoseconds=1` |
| Bool alternative and guard | `value:type="bool"`, `value:[bool]:not` |
| Duration comparison | `value="1ns":duration` |
| Bool comparison | `value=true` |
| Union compared with itself | `value=value` |
| Membership across alternatives | `value:in("1ns":duration,7)` |
| Duration membership with union candidates | `"1ns":duration:in(value)` |
| List-element union comparison | `items[0]=items[1]` |
| List membership and callback | `items:contains(true)`, `items:all(x=>x)` |
| Collection and callback type | `items:unique`, `items:map(x=>x:type):contains("bool")` |
| `string()` output | `value:string="0.000000001s"` |
| `json()` output | `value:json="\"0.000000001s\""` |

For `Duration|Float|Null`, the supported subset includes type inspection,
type guards, comparisons, and membership. For example, these expressions
dispatch using the JSON category:

```hff
value:type="duration"
value:[duration]:nanoseconds=1
value:[float]>0.5
value=value
value:in("1ns":duration,1.5)
```

The Float branch's `type()` result is `number`; use `[float]` to distinguish it
from Int. JSONB needs no numeric type metadata to distinguish the string-backed
Duration branch from the numeric Float branch. Native JSON preserves the
Float's signed zero for `string()` and `json()` serialization. Build rejects
those serialization operations for JSONB when signed-zero provenance may have
been erased.

The [JSON union example](https://github.com/ornew/hff/tree/main/examples/sql-json-unions)
shows schema-known Timestamp/Duration unions with Int, Float, and null
alternatives, JSONB metadata bindings, and scalar/list predicates. It builds
parameterized SQL without connecting to a database.

This is a limited supported subset, not general tagged-union support. Other
unions and operations may be rejected by `Build`, especially when the
translator cannot recover the active HFF type. For example,
`String|Duration` and `Bytes|Duration` share the JSON string representation
and must be rejected when an operation needs their identity.
JSONB metadata version 1 restores numeric types only; it does not add tags for
these string-backed alternatives. Ambiguous non-null tagged unions,
overlapping structural alternatives such as `List<Duration>|List<Any>`, and
operations outside the supported subset remain Build errors.

```sql
CREATE TABLE job_events (
    id bigint PRIMARY KEY,
    profile jsonb NOT NULL
);
INSERT INTO job_events VALUES
    (1, '{"elapsed":"1m30s","splits":["1s","2.5s"],"summary":{"elapsed":"0s"}}'::jsonb),
    (2, '{"elapsed":"45s","splits":["0.5s"],"summary":{"elapsed":"1s"}}'::jsonb);
```

Declare the JSON shape, bind the whole `profile` value, and query its typed
Duration fields. The surrounding service supplies `ctx` and its database
handle:

```go
profileType := schema.Struct(
    schema.Duration("elapsed"),
    schema.List("splits", schema.DurationType),
    schema.Field("summary", schema.Struct(schema.Duration("elapsed"))),
)
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.Field("profile", profileType))),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}
program, err := e.Compile(ctx,
    `profile.elapsed:seconds>=60 profile.splits:contains("1s":duration) profile.summary.elapsed:nanoseconds=0`,
)
if err != nil {
    return err
}
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindJSON(
        hff.Path{"profile"}, hffsql.Column("job_events", "profile"), hffsql.JSONB,
    )),
)
if err != nil {
    return err
}
rows, err := db.QueryContext(ctx,
    `SELECT job_events.id FROM job_events WHERE `+query.Where(), query.Args()...)
```

The predicate matches row 1. JSON null maps to HFF null where the schema allows
it; nested objects, lists, and maps retain their declared Duration members.
Duration values support comparison, equality, membership, sorting, folds, and
the methods supported for their schema-known representation when the
surrounding result types remain representable. `string()` returns the
canonical duration text, and `json()` returns that text as a JSON string. The
[temporal guide](../../guides/temporal.md#parse-and-measure-durations) describes
the canonical format and parser rules. Stored values must satisfy the declared
schema; `Build` does not inspect JSON documents.

### Timestamp values in JSON documents

When the schema declares a Timestamp, JSON and JSONB store it as its canonical
UTC RFC 3339 string. The canonical form uses a four-digit year, `Z`, at most
nine fractional-second digits, and omits trailing fractional zeros. It
represents an instant, not the source offset. A JSON string under `Any` remains
a String; the schema supplies Timestamp identity. The caller guarantees that
stored values are canonical and match the declared schema. `Build` does not
read or validate stored documents.

For example, this JSONB document stores a nanosecond-precision event time:

```sql
CREATE TABLE json_events (
    id bigint PRIMARY KEY,
    payload jsonb NOT NULL
);
INSERT INTO json_events VALUES
    (1, '{"created":"2026-10-01T00:00:00.000000123Z"}'::jsonb),
    (2, '{"created":"2026-10-01T00:00:00.000000122Z"}'::jsonb);
```

Declare the nested field as Timestamp and bind the complete document:

```go
payloadType := schema.Struct(schema.Nullable(schema.Timestamp("created")))
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.Field("payload", payloadType))),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}
program, err := e.Compile(ctx,
    `payload.created:after("2026-10-01T00:00:00.000000122Z":timestamp)`,
)
if err != nil {
    return err
}
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindJSON(
        hff.Path{"payload"}, hffsql.Column("json_events", "payload"), hffsql.JSONB,
    )),
)
if err != nil {
    return err
}
rows, err := db.QueryContext(ctx,
    `SELECT json_events.id FROM json_events WHERE `+query.Where(), query.Args()...)
```

Executing that predicate selects row 1: its Timestamp is one nanosecond after
the comparison value. Translation parses the canonical string into exact
PostgreSQL `numeric` total epoch nanoseconds. The value is not limited to
`int64`, which preserves HFF years 0000 through 9999 without routing the
fraction through PostgreSQL's microsecond-precision `timestamptz` storage.

For schema-known JSON/JSONB Timestamps, the PostgreSQL subset supports direct
comparisons, `before()`, `after()`, `add(duration)`, `in()`, `exists()`,
`type()`, UTC component methods, and zero-argument `string()` and `json()`.
Schema-known lists of Timestamps also support ascending `sort()` by exact
instant, `contains()`, and `unique()`. Timestamp values can also be retained in
schema-known list and struct construction and serialized with `json()` when
the result schema is representable. For example, a typed list can compare,
deduplicate, and serialize its Timestamp elements:

```hff
items:contains("2026-10-01T00:00:00.000000123Z":timestamp)
items:unique
items:json="[\"2026-10-01T00:00:00.000000123Z\"]"
[payload.created]:json="[\"2026-10-01T00:00:00.000000123Z\"]"
```

The same canonical string representation is used when a schema-known struct
containing Timestamp fields is serialized. A JSON string under `Any` remains
String; these operations do not infer Timestamp values from JSON strings.
For schema-known Timestamp lists, a bounded callback subset can also construct
new Timestamp values and serialize or search the result. This includes mapping
`add(duration)` over elements, then using `json()` or `contains()`, and
flatmapping each result into a singleton list. For example, with `items` bound
as a JSON or JSONB `List<Timestamp>` containing
`["0000-01-01T00:00:00Z"]`:

```hff
items:map(x=>x:add("1ns":duration)):json="[\"0000-01-01T00:00:00.000000001Z\"]"
items:map(x=>x:add("1ns":duration)):contains("0000-01-01T00:00:00.000000001Z":timestamp)
items:flatmap(x=>[x:add("1ns":duration)]):json="[\"0000-01-01T00:00:00.000000001Z\"]"
```

Adding one nanosecond to `9999-12-31T23:59:59.999999999Z` yields null rather
than an out-of-range Timestamp. The output of `string()` and `json()` is
canonical UTC text. Other collection
transformations may still be rejected when their SQL lowering cannot represent
the required Timestamp value.

The schema-known disjoint unions `Timestamp|Bool|Null` and
`Timestamp|Float|Null` are also supported for `type()`, explicit Timestamp
guards, comparisons, and membership. Typed lists of these unions support
`contains()`, `unique()`, and callback cases where the resulting types are
representable.
JSON strings identify Timestamp, booleans identify Bool, numbers identify
Float in the second union, and JSON null remains null. The previously supported
`Timestamp|Int|Null` union follows the same rule for its integer alternative.
For a `Timestamp|Bool|Null` schema on `value` and `items`, a filter can inspect
the active branch and search the list:

```hff
value:type="timestamp"
value:[timestamp]:year=2026
value:in("2026-01-01T00:00:00.000000123Z":timestamp,true)
value=value
items:contains(true)
items:unique
items:map(x=>x:type):contains("timestamp")
```

With a `Timestamp|Float|Null` schema, the numeric alternative is Float. Its
HFF `type()` result is `"number"`, so use a type guard to test specifically for
Float:

```hff
value:[float]:exists
value:in("2026-01-01T00:00:00.000000123Z":timestamp,1.5)
items:contains(1.5)
```

The `Timestamp|Int|Null` subset also supports `string()` and `json()` when the
active value can be serialized faithfully:

```hff
value:string="2026-01-01T00:00:00.000000123Z"
value:json="\"2026-01-01T00:00:00.000000123Z\""
```

For `Timestamp|Int|Float|Null` and `Duration|Int|Float|Null`, native JSON
spelling preserves the Int-versus-Float distinction. JSONB requires
`WithTypeMetadata` for operations that observe that distinction; Build rejects
those operations when the sidecar is omitted. The existing version-1 sidecar
restores numeric kinds and values, including signed zero when its optional
`text` is present. Both unions support type inspection, explicit guards,
comparisons, membership, list `contains()`/`unique()`, and `string()`/`json()`
serialization when the stored numeric metadata preserves the needed value.
These are supported operations, not a guarantee that every collection
transformation is available.

For example, these JSONB scalars can be a Timestamp, Int, Float, or null. The
root JSON Pointer `""` identifies a scalar value in its sidecar:

```sql
CREATE TABLE temporal_values (
    id bigint PRIMARY KEY,
    value jsonb NOT NULL,
    value_types jsonb NOT NULL,
    items jsonb NOT NULL,
    items_types jsonb NOT NULL
);
INSERT INTO temporal_values VALUES
    (1, '"2026-10-01T00:00:00.000000123Z"', '{"version":1,"types":{}}',
        '["2026-10-01T00:00:00.000000123Z",9007199254740992]',
        '{"version":1,"types":{"/1":{"type":"float","text":"9007199254740992.0"}}}'),
    (2, '9007199254740993', '{"version":1,"types":{"":{"type":"int"}}}',
        '[7,"2026-10-01T00:00:00.000000123Z"]',
        '{"version":1,"types":{"/0":{"type":"int"}}}'),
    (3, '9007199254740992', '{"version":1,"types":{"":{"type":"float","text":"9007199254740992.0"}}}',
        '[-0.0,0]', '{"version":1,"types":{"/0":{"type":"float","text":"-0.0"},"/1":{"type":"int"}}}'),
    (4, '-0.0', '{"version":1,"types":{"":{"type":"float","text":"-0.0"}}}',
        '[null]', '{"version":1,"types":{}}'),
    (5, 'null', '{"version":1,"types":{}}', '[]', '{"version":1,"types":{}}');
```

For a JSONB list, pointers are relative to that list's document. For example,
`["2026-10-01T00:00:00.000000123Z",9007199254740992]` can use
`{"version":1,"types":{"/1":{"type":"float","text":"9007199254740992.0"}}}`.
The Float marker preserves the numeric subtype, and `text` preserves the
originally significant spelling or signed zero when later serialization needs
it. The caller keeps each sidecar consistent with its corresponding JSONB
column.

Declare the alternatives and bind the data and sidecar columns together:

```go
valueType := schema.Union(
    schema.TimestampType, schema.IntType, schema.FloatType, schema.NullType,
)
itemsType := schema.ListOf(valueType)
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.Field("value", valueType),
        schema.Field("items", itemsType),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}
program, err := e.Compile(ctx,
    `value:in("2026-10-01T00:00:00.000000123Z":timestamp,1.5) | items:contains(1.5) | items:unique`,
)
if err != nil {
    return err
}
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindJSON(
        hff.Path{"value"},
        hffsql.Column("temporal_values", "value"),
        hffsql.JSONB,
        hffsql.WithTypeMetadata(hffsql.Column("temporal_values", "value_types")),
    ), hffsql.BindJSON(
        hff.Path{"items"},
        hffsql.Column("temporal_values", "items"),
        hffsql.JSONB,
        hffsql.WithTypeMetadata(hffsql.Column("temporal_values", "items_types")),
    )),
)
```

`value:[timestamp]:year=2026`, `value:[int]:exists`, `value:[float]:exists`,
`value=value`, `items:contains(1.5)`, and `items:unique` are supported for both
Timestamp and Duration mixed-numeric schemas when the numeric representation is
recoverable. The same schemas can bind native JSON without a sidecar because
its numeric spelling distinguishes Int from Float. The [runnable JSON union
example](https://github.com/ornew/hff/tree/main/examples/sql-json-unions)
builds parameterized predicates for scalar and list Timestamp/Duration mixed
unions using JSONB sidecars; it does not connect to a database. Without
metadata, operations on a mixed-union value bound to raw JSONB fail at Build
when they need to distinguish Int from Float. A separate Float-only JSONB
binding can still be compared with a mixed-union value bound as native JSON;
the Float schema fixes the first operand's numeric type.

For JSONB, Float `string()` and `json()` serialization use the sidecar's `text`
when needed to preserve negative zero or another otherwise-lost value. For
example, the `-0.0` row above needs that text. A Float-only JSONB binding can
be compared against a mixed-union value bound as native JSON; metadata is
required on a mixed-union JSONB binding whose active numeric subtype would
otherwise be ambiguous.
Unions whose alternatives share a JSON representation category, such as
`String|Timestamp` or `Duration|Timestamp`, also remain ambiguous. `Build`
rejects operations when it cannot recover the active HFF type or preserve the
requested result. Other dynamic union operations remain supported only when
the schema and JSON representation preserve the HFF type.

Time-zone arguments—including `timestamp:string(tz)`—and data-dependent string
parsing are unsupported. Operations outside the listed subset are rejected
when their SQL lowering cannot preserve the Timestamp representation.
JSONB metadata version 1 remains numeric-only and does not add Timestamp tags.
For native `timestamptz` columns, use the separate `BindTimestamp` contract in
the [temporal guide](../../guides/temporal.md#store-postgresql-timestamps).

### Bytes in JSON documents

When the schema declares a field as Bytes, its JSON representation is a
canonical standard-base64 string. The schema supplies the Bytes identity;
ordinary JSON strings are not inferred to be Bytes. Nullable Bytes fields use
JSON null. JSONB preserves the base64 text, so Bytes do not need numeric type
metadata. Store only canonical padded standard-base64 values that decode
strictly; `Build` does not scan stored documents to verify them.

Schema-known Bytes values work in nested structs, maps, and lists, including
list callbacks, JSON construction, and `json()` serialization as base64 JSON
strings. Typed Bytes equality and `in()` membership work when the compared
values have statically known Bytes types. A Bytes fold seed or callback value
is also supported when the accumulator and callback types remain explicit; an
`Any` accumulator remains JSON-native and cannot carry Bytes identity. Generic
dynamic tree comparisons involving a Bytes-bearing value remain unsupported,
such as comparing a Bytes value through a generic path with an `Any`
counterpart. A statically known Bytes field can coexist with unrelated `Any`
fields in the same fold.

Typed map operations including `size()`, `has()`, `get()`, `keys()`, and
`values()` work with Bytes values, and callbacks can process those values as
Bytes. `items()` has a narrower limitation: it forms positional `[key, value]`
pairs whose element types are String and Bytes, but the current representation
cannot retain those position-specific types. Serialization or downstream
operations that need to recover the Bytes identity may therefore be rejected
at `Build` time. This does not make Bytes values inside an `Any` representation
supported; `Any` remains JSON-native. Mixing Bytes positions with `Any` or
String positions in the same `items()` result is also unsupported.

The schema-known `Bytes|Int|Float|Null` and
`Bytes|Bool|Int|Float|Null` unions are supported for native JSON and for JSONB
with numeric type metadata when Int/Float identity is needed. The
category-disjoint `Bytes|Bool` and `Bytes|Bool|Null` unions need no numeric
metadata. JSON strings select Bytes, booleans select Bool when present, and
numbers select the numeric alternatives. Supported operations include
`type()`, explicit type guards, equality and inequality, numeric ordering on the
active numeric branch, `in()`, list
`contains()`/`unique()`, serialization, and `map()` to `type()`. `len()` on an
active Bytes value counts its decoded bytes. `sort()` orders numeric-only lists
with stable Int/Float ties; a Bytes, Bool, null, or other nonnumeric element
makes the result ordinary null. Bytes themselves are not orderable. For example,
if `value` and `items` have this union schema:

```hff
value:type="bytes"
value:[bytes]:exists
value:len=4
value="AAECAw==":frombase64
value:[float]:exists value>1.5
value!=7
value:in("AAECAw==":frombase64,7)
items:contains("AA==":frombase64)
items:unique
items:map(x=>x:type):contains("bytes")
items:sort:json="[1,7.0]"
value:string="AAECAw=="
value:json="\"AAECAw==\""
```

Native JSON preserves the Int/Float spelling. Raw JSONB cannot distinguish
those numeric alternatives and is rejected at Build when an operation needs
that distinction; bind the version-1 numeric sidecar in those cases. These
supported shapes do not imply support for every union containing Bytes. The
listed Bool combinations are recoverable because JSON strings, booleans, and
numbers have distinct categories; operation support remains specific to each
lowering. `String|Bytes` is ambiguous because both alternatives use JSON
strings. Structural type-guard merging does not recover an identity that the
stored representation cannot distinguish.

For example, `payloads` can be a JSONB array of Bytes strings:

```sql
CREATE TABLE binary_assets (
    id bigint PRIMARY KEY,
    payloads jsonb NOT NULL
);
INSERT INTO binary_assets VALUES
    (1, '["AAECAw==", "AA=="]'::jsonb),
    (2, '["AA=="]'::jsonb);
```

With `payloads` declared as `List<Bytes>`, this predicate selects the first
row because one item is four bytes long and the list contains the decoded
four-byte value:

```hff
payloads:any(p=>p:len>3) payloads:contains("AAECAw==":frombase64)
```

The [runnable JSON Bytes example](https://github.com/ornew/hff/tree/main/examples/sql-json-bytes)
shows the matching schema and `BindJSON` configuration. Ambiguous dynamic
representations are rejected: a JSON string under `Any` is a String, and a
`String|Bytes` union cannot distinguish its alternatives from JSON storage.
The backend rejects operations that need to treat such unrecoverable values as
Bytes rather than guessing.

### Parse JSON text with `fromjson()`

For a string receiver, `fromjson()` parses one complete JSON document and
returns its JSON-native HFF value. A recoverable dynamic JSON string is also
accepted; a dynamic receiver of another runtime family returns ordinary null.
The root and all nested arrays, objects, and numbers are validated before the
result is exposed. Invalid syntax, duplicate object keys, invalid Unicode
escapes, int64 overflow, and non-finite or overflowing float values produce
ordinary null for the entire document. No partial value is returned. JSON
whitespace around the single root value is accepted. The PostgreSQL Float
profile rejects values it classifies as overflow or underflow; if such a
number appears anywhere in a parsed document, the entire `fromjson()` result
is null. The local backend instead accepts underflow to signed zero, as
reported by its profile.

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

When the same projected string receiver is passed to `fromjson()` more than
once in a projection scope, PostgreSQL SQL can reuse the fully validated JSON
document. Each call still evaluates its own presence state, and callback or
shadowed-alias scopes are isolated. Different SQL expressions are not combined
based on equal runtime string contents. This reuse does not skip validation of
unused nested fields. See the
[measured parsing and validation comparison](../../optimizations/004-postgresql-json-validation-reuse.md)
for the workload, generated-SQL tradeoffs, and sample limits.

Repeated unguarded literal-field chains from the same parsed document can also
share field extraction and decoding within that scope. For example, the two
uses of `data:fromjson.a` here can share the parsed carrier and selected field:

```hff
data:fromjson.a=1 data:fromjson.a<2
```

The full source document is still validated. Guarded or type-guarded roots,
indexing, extensions, arbitrary computed expressions, and separate callback
scopes do not inherit this reuse. See
[parsed JSON field reuse](../../optimizations/036-postgresql-reuse-parsed-json-fields.md) for the cache
boundaries and SQL-shape evidence.

### Convert strings with `bool()` and `number()`

`bool()` accepts exactly lowercase `true` or `false`; it is case-sensitive and
does not trim surrounding whitespace. `number()` accepts one strict JSON
number spelling, also without surrounding whitespace. Integer spellings must
fit int64 and return int64; a decimal point or exponent selects float64. Invalid
spellings, integer overflow, Float inputs rejected by PostgreSQL, and
non-finite results return ordinary null. PostgreSQL's `float.parsing` profile
detail specifies its native input behavior, including underflow. A null or
inapplicable receiver returns null, while no-value propagates without invoking
the conversion.

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

Float is approximate, and PostgreSQL follows the `float.*` model declared in
the built query's Profile. Ordinary Float arithmetic uses PostgreSQL
`double precision`; extreme magnitudes may use a checked `numeric` fallback
to prevent database errors or non-finite results. The fallback may round or
produce a different zero sign from PostgreSQL's native Float operation.
Division by zero, overflow, invalid results, and non-finite results yield
null. Int-only arithmetic remains exact, and mixed Int/Float comparisons
remain exact over the represented values. See the
[backend-native Float design](../../design/021-backend-native-float-semantics.md)
for the language contract and backend profile details.

For a dynamic JSON value, the compiled schema and any compiled type guard
restrict which numeric families can reach an operation. The SQL translator
uses those restrictions after its existing stored-value and metadata checks;
it omits only the unreachable arithmetic branch. For example, `i` declared as
`String|Int|Null` has only an Int input family, while a
`String|Float|Null` value retains PostgreSQL's general Float kernel:

```hff
i+2>0
f+2.0>0
(data:[string|int]+2):type="int"
i/2>0
```

An `Int|Float` value or `Any` value keeps every numeric family it may contain.
Mixed arithmetic still promotes Int as specified, and Int division still
returns Float even though its input family is Int. These restrictions do not
validate stored rows at Build time;
the declared schema, JSON representation, and any required JSONB metadata
must remain consistent. This SQL-shape optimization does not alter the
PostgreSQL-native Float Profile or promise an execution-speed improvement.
See [PostgreSQL translation optimizations](optimizations.md#prune-numeric-arithmetic-branches-excluded-by-json-schema)
for the shape evidence and applicability limits.

When the compiled result type is exactly one non-null numeric family, SQL
arithmetic can keep that result as `bigint` or `double precision`. For example,
`i+2` with `i: String|Int|Null` has an Int result, while `i/2` has a Float
result even though both inputs are Int. A mixed expression such as `i+f` also
has one Float result after the required promotion. In these cases the
translator can skip serializing the computed result to JSON and decoding it
again. It retains the original representation checks, JSONB metadata recovery,
presence, and arithmetic kernels.

Nullable Int and nullable Float results also use this path. Expressions with
multiple numeric subtypes retain JSON to recover their runtime subtype;
Null-only and Never results keep their existing untyped null carrier.
This changes generated SQL shape, not
the PostgreSQL Float Profile or HFF arithmetic rules. See [typed JSON numeric
results](optimizations.md#keep-single-family-json-arithmetic-results-typed)
for filters, comparison limits, and plan-shape evidence; no execution timing
was measured.

Float arithmetic lowering follows the PostgreSQL `float.*` Profile, including
the documented extreme-magnitude fallback. It does not promise a particular
SQL size or execution time. The earlier exact-decomposition reuse measurement
describes a superseded implementation, not a current guarantee.

### Numeric rounding methods

`round()`, `ceil()`, and `floor()` are supported for schema-known `Int` and
`Float` values and for JSON `Any` or unions when the runtime numeric subtype is
recoverable. Integer receivers keep their int64 value and subtype. Float
receivers keep the Float subtype; their `round()`, `ceil()`, and `floor()`
behavior follows PostgreSQL's `float.rounding` Profile detail. Null or
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

### Parse computed durations

PostgreSQL supports `duration()` on a text column or a recoverable JSON string.
It produces a computed HFF Duration represented internally as signed int64
nanoseconds. This computed conversion is separate from native Duration
columns, which use `bigint` nanoseconds, and from schema-known JSON/JSONB
Duration storage described [above](#duration-values-in-json-documents). The
local duration syntax and range rules in the
[temporal guide](../../guides/temporal.md#parse-and-measure-durations) also
apply to this conversion.

```sql
CREATE TABLE jobs (id bigint PRIMARY KEY, elapsed_text text NOT NULL);
INSERT INTO jobs VALUES (1, '45s'), (2, '1m30s'), (3, 'invalid');
```

With `elapsed` declared as a string and bound to `jobs.elapsed_text`, this
predicate matches only row 2:

```hff
elapsed:duration:seconds>=60
```

Computed durations can also be tested against Duration candidates with `in()`:

```hff
elapsed:duration:in("1m":duration,"1h":duration)
```

The [runnable SQL duration example](https://github.com/ornew/hff/tree/main/examples/sql-durations)
shows the schema and binding and builds the parameterized predicate without
connecting to a database. The caller places `Where()` and `Args()` into its own
query to execute it.

The parser sums decimal components exactly before checking integral-nanosecond
precision and int64 bounds. Long fractional spellings are not truncated to a
fixed decimal scale. Invalid syntax, sub-nanosecond totals, and overflow
produce ordinary null. A null or non-String dynamic receiver also produces
ordinary null; no-value propagates. Computed Duration values support truth,
equality and ordering comparisons, type checks, and canonical `string()` and
`json()` conversion. `nanoseconds()` returns int64; other unit methods first
convert the nanosecond count to float64 and then divide by the unit expressed
as float64. `in()` accepts computed Duration and null candidates; known
non-Duration candidate types simply do not match. If a dynamic candidate's
schema can include Duration but its runtime type cannot be recovered,
`Build` rejects the plan. A reached no-value candidate propagates even when
another candidate has a different known type. PostgreSQL does not enforce the
local parser's `MaxSteps` budget; database timeouts and input-size limits
remain the caller's responsibility.

### Schema-known collection access

Known-schema field access is supported for nested struct/map fields. List
indexing accepts negative indexes from the end and integer-valued floating
indexes. A fractional or out-of-range index produces a null result. Missing
object keys can be checked with `has()`; `get()` returns null for a missing
key, so use `has()` when that distinction matters. JSON null remains a value
and propagates through the ordinary HFF evaluation rules.

For schema-known JSON objects, the backend supports `size()`, `has(key)`,
`get(key)`, `keys()`, `values()`, and `items()`. Enumeration orders keys with
PostgreSQL's `C` collation. For schema-known JSON lists, it supports `size()`,
`count()`, `sum()`, `filter(predicate)`, `has(index)`, `contains()`, `unique()`,
`sort()`, `concat(list)`, `any()`, `all()`, `map()`, and `flatmap()` when the
callback and resulting values can be represented by the binding. `filter()`
preserves JSON list order and does not need a separate order mapping.
`contains()` requires every supplied needle to be present; a null
needle is an ordinary value, while a null list receiver propagates null. A
no-value argument propagates even if an earlier needle matched. `unique()`
compares each element
by HFF equality, so repeated stored nulls and repeated nested composite values
are duplicates. These operations require applicable list/object equality
capabilities for the types actually compared.

`sum()` is currently limited to Int contributions for JSON/JSONB lists. The
list element type, or the callback result type, must be statically limited to
Int and null; guarded no-value callback results are also skipped. Empty and
all-skipped lists return Int zero, and an out-of-range final total returns
null. Float or unresolved dynamic contributions are rejected by `Build`. This
PostgreSQL limitation does not change the language rule that Float sums follow
the backend's declared Float model; see the
[native Float design](../../design/021-backend-native-float-semantics.md).

### Containment for simple JSONB filters

For a directly bound JSONB object without type metadata, a literal-key equality
filter is supported when the value is a literal String or Bool:

```hff
payload.team="auth"
payload.enabled=true
```

The filter lowers to an object-containment predicate of the form
`column IS NOT NULL AND column @> jsonb_build_object(key, value)`, with the key
and value kept as bound parameters. A SQL-null document, missing key, JSON-null
value, or different value does not match. This path is limited to one literal
key and a direct root JSONB binding without metadata. Inequality, null
equality, numeric values, nested paths, JSON text bindings, computed receivers,
and metadata-backed bindings keep the general lowering.

For a directly bound JSONB list whose declared element type is non-nullable
String, a simple equality callback can express that every element equals a
String literal:

```hff
tags:all(item=>item="auth")
tags:all(item=>"auth"=item)
```

The supported form lowers to `column IS NOT NULL AND column <@
jsonb_build_array(value)`. An empty list matches because every element
satisfies the predicate; duplicate elements do not change the result. At the
filter boundary, a null list does not match. Nullable element types and complex
callbacks keep the general lowering. In the checked plans, object `@>` selected
a Bitmap Index Scan on a temporary GIN-indexed fixture. List `<@` remained a
Seq Scan even with sequential scans disabled. This is plan evidence only, not a
runtime-speed claim.

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

For a schema-known Map, `get(key)` validates the applicable Map alternatives,
selects the raw value, then decodes or normalizes only that candidate. A literal
key has a known result schema, so only the selected value schema is checked;
unused Struct fields do not impose representation requirements. If the
selected value has one primitive type, a JSON-category check followed by a
direct decode preserves wrong-category and null behavior without normalizing a
comparison document. A selected union or nested Map still needs
schema-guided normalization, but only after selection. Dynamic keys across
Map alternatives validate the original Map schemas before merging their value
types, because merging can otherwise hide an unsupported representation.
For example, a value declared as Float remains Float when its JSONB spelling
is `1`; the declared subtype guides decoding without guessing from the text.
Known Map/Struct carriers also avoid a redundant receiver-category check under
the declared storage contract. Existing JSON-null normalization preserves
nullable and guarded-key behavior.

Dynamic `get(key)` on heterogeneous Struct alternatives and `values()` or
`items()` still normalize the complete object for per-key dispatch or because
they observe multiple values. Native JSON retains JSON-native runtime types for
`Any`; JSONB `Any` requires metadata when a selected or enumerated numeric
subtype must be restored. Metadata restores numeric representations only; it
does not add support for bytes, timestamps, or durations inside dynamic JSON
values. If the selected result cannot be determined from the schema and
available representation information, `Build` rejects the query. Static dot,
bracket, and selected JSONB metadata-subtree access keep their existing paths;
see [selected metadata restoration](optimizations.md#restore-only-a-selected-jsonb-metadata-subtree).
The [selected-value SQL-shape record](optimizations.md#select-a-json-object-value-before-normalization)
describes the measured access cases without claiming execution speed.

### Sort JSON-backed lists

`sort()` returns a stable ascending copy. Schema-known integer and float lists
sort numerically; strings sort by Unicode code point using PostgreSQL's `C`
collation; schema-known Timestamp lists sort by exact instant. Equal values
retain their input order. A runtime list containing
null or incompatible/non-orderable values produces null. A statically known
type with no orderable member is rejected during compilation. A
dynamic `Any` or list union can also be sorted when its stored list and
element representations are recoverable. Dynamic lists may contain all
numbers (including mixed int64 and float64) or all strings; mixed families,
null elements, and other non-orderable elements produce null. For JSONB, supply
numeric type metadata when normalization has erased the int64/float64
distinction. Sorting is stable for equal numeric values, so original order and
numeric representations such as signed zero are retained:

```hff
data:sort=[1,2.0,3.0]
data:sort[0]:[int]:exists
data:sort[1]:[float]:exists
data:sort:json="[1.0,1]"
```

For `data` containing `[2.0,1,3.0]`, the first three predicates observe the
sorted list `[1,2.0,3.0]` and its numeric subtypes. For `data` containing
`[1.0,1]`, the final predicate checks that equal numeric values retain input
order when their representations are recoverable. The same query can use an
`Any` field bound to a JSON or JSONB column. Native JSON retains JSON-native
numeric types; JSONB needs the configured metadata when the stored values
could be either int64 or float64. A schema-known Float type identifies the
numeric subtype, but it cannot recover a negative-zero sign lost during JSONB
normalization; include the Float text metadata if that sign must remain
observable. See the [storage requirements](#storage-requirements-and-limits)
for the sidecar format. The scoped Build and database measurements are in the
[dynamic sorting record](../../optimizations/011-postgresql-dynamic-json-sort.md).

Schema-known list element unions such as
`Timestamp|Int|Float|Null` and `Duration|Int|Float|Null` can also use
`sort()`. A list whose elements are all temporal strings sorts by exact
Timestamp or Duration value; a list whose elements are all numbers sorts
numerically, including mixed Int and Float values. Equal numeric values retain
their input order and representation, including signed zero. A list mixing a
temporal string with a number, or containing null or another non-orderable
value, produces ordinary null. JSONB mixed Int/Float values require numeric
type metadata when their subtype is needed; raw JSONB mixed-subtype bindings
remain Build errors.

Selected list callbacks are also supported for these schema-known unions:
`any()` and `all()` with type guards, `map()` to `type()`, `string()`, or the
identity value, identity `flatmap()`, and `foldl()`/`foldr()` with an Int guard.
The callback result must have a supported representation. In particular,
guarded no-value from a fold item propagates through the fold. Native JSON or
JSONB with the required numeric metadata preserves Int/Float identity and
signed-zero details where those results observe them; raw JSONB remains a
Build error when it has erased a required distinction. This list is a tested
subset, not support for arbitrary callback expressions over temporal/numeric
unions. See the [temporal guide](../../guides/temporal.md#store-schema-known-timestamps-in-json-or-jsonb)
for examples.

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

Callback outputs are checked against their inferred schema types before being
retained or aggregated. For `map()` and fold seeds/results, a newly computed
native SQL scalar can be serialized directly into JSON state without parsing
and normalizing that scalar again when its complete stored schema is one
primitive family, optionally nullable, matching its SQL carrier. This
applies to Bool, Int, Float, String, Bytes, Duration, and Timestamp. SQL `NULL`
becomes JSON `null`, while presence and no-value remain separate. Float-loss
handling and schema representability checks are unchanged. JSON carriers, `Any`, wider
unions, containers, and `flatmap()` results keep their ordinary normalization
path. See the
[constructed callback JSON record](../../optimizations/031-postgresql-constructed-callback-json.md)
for its scope and SQL-shape evidence.

A constant string result such as `map(x=>"fixed")` does not need numeric
metadata. An operation that observes a numeric distinction JSONB has erased,
such as an integer-versus-float type guard, or retains an ambiguous numeric
value, requires native JSON or sufficient JSONB metadata. Truthiness and
`exists()` do not inherently need numeric subtype metadata. `flatmap()` restores
the callback's list candidates; an irrelevant scalar integer/float alternative
does not by itself require metadata. As elsewhere, unsupported execution-budget
requirements are rejected by `Build`.

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

When a fold's seed and callback result normalize compatibly to its inferred
accumulator schema, PostgreSQL can reuse the normalized predecessor inside
the recursive callback. This changes the generated SQL only: full schema
checks, fold result types, SQL NULL and JSON null, and no-value propagation
remain in force. Guards, extraction, casts, and incompatible merged shapes
use ordinary normalization. Reuse is permitted only after a separate
value-stability check against the target schema; a proof retains the original
stored type and does not imply canonical serialization. See the
[fold normalization reuse record](../../optimizations/035-postgresql-reuse-normalized-fold-json.md)
for the compatibility conditions and measured SQL-shape scope.

PostgreSQL does not provide local `MaxSteps` or `MaxGeneratedValues` enforcement
or a guarantee about physical callback evaluation order. JSONB metadata and
these dynamic folds do not infer Duration inside `Any`; schema-known Duration
values use the JSON/JSONB representation described above.

```hff
profile.scores:foldl(0,(acc,score)=>acc-score)=-170
profile.scores:foldr(0,(score,acc)=>score-acc)=-10
```

These are HFF result-order guarantees. SQL does not promise physical callback
evaluation order or local `MaxSteps`/`MaxGeneratedValues` enforcement; unsupported
explicit execution-budget requirements are rejected by `Build`.

For schema-known JSON/JSONB values, `json()` and `string()` also support
Duration: `string()` returns the canonical duration text, while `json()` returns
it as a JSON string. A string under `Any` remains a String; this conversion
depends on the declared schema.
For schema-known scalar columns and JSON/JSONB values, `json()` and
`string()` support null, bool, int64, string, and finite float64 values. Native
Duration scalar columns and schema-known JSON/JSONB Duration values also
support these methods, producing canonical duration strings.
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

Float serialization uses PostgreSQL's native Float text as reported by
`float.format`. JSON transport requires `extra_float_digits > 0`, which is the
PostgreSQL default; HFF does not set or inspect that session setting. JSON
output retains a decimal or exponent marker so the Float subtype is preserved.
JSONB metadata is still required when storage has erased an observable Float
representation, such as negative zero. Exact decimal spelling is not
guaranteed to match another backend.

The canonical JSON token walk retains scalar and null rows as values while
skipping their child expansion. It also decodes each Float token once before
formatting. This lowers generated SQL for the measured dynamic JSON shapes;
typed controls do not use that token walk. See [the canonical serialization
measurements](../../optimizations/009-postgresql-canonical-json-lowering.md)
for the tested cases and limits. PostgreSQL execution still follows database
behavior and does not enforce the local runtime's JSON-depth limit.

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

`string.frombase64()` and `string.frombase64r()` are explicit conversions for
string fields and recoverable JSON string values. They produce a scalar Bytes
expression without metadata. They do not infer Bytes for an untyped JSON
string; stored schema-known Bytes use the declared schema and canonical base64
representation described [above](#bytes-in-json-documents). See the
[PostgreSQL bytea support](README.md#bytes-and-base64) for native column
methods and null rules.

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

The same runtime type rules apply to an `Any` value reached through a map
field. For a nullable `Map<Any>` named `payload`, a JSONB Float sibling can
use numeric metadata while a JSON string at `payload.left` remains a String:

```hff
payload.left="a" payload.right="b"
```

Metadata for a numeric pointer such as `/f` restores only that numeric value;
it does not change the type of the sibling string field.

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
that observe the exact representation, such as serialization. For scalar
dynamic pairs, PostgreSQL can compare the original JSON values or
metadata-restored JSONB values without serializing them to canonical JSON first.
This does not skip schema, representability, or metadata checks. When compatible
list or object values are possible, structural equality retains the recursive
path. See [scalar pair SQL shape](optimizations.md#compare-scalar-json-pairs-without-canonical-json-conversion)
for details.

Repeated numeric comparisons can reuse matching Float floor and Int/Float
ordering projections within one projection scope. Inside one scalar JSON pair,
identical stored text can share a numeric decode only when its family,
ambiguity requirement, and Float override match. Each comparison still applies
its own operator, direction, and presence behavior; callback and filter scopes
remain isolated. These translation details do not change JSON or JSONB
representation requirements. See [numeric comparison kernel reuse](optimizations.md#reuse-numeric-comparison-kernels-within-a-scope)
for SQL-shape evidence and limits.

An incompatible runtime family at the comparison root produces ordinary HFF
null, which matches no row as a predicate. A runtime-null or no-value operand
compares false. Within a list or object comparison, a mismatch between nested
value families makes the structures unequal; a stored null equals a stored
null at the same location, while a missing object key differs from a present
null key. This preserves the distinction between a top-level dynamic type
mismatch and a recursive structural mismatch.

When both JSON-backed operands have definite roots in the same structured
category, PostgreSQL can use typed recursive equality even when nested values
are `Any`. For root `=` and `!=` between finite compiled unions, the translator
can also specialize overlapping alternatives at each reachable element or
field position. Lists compare by ordinal; objects compare actual keys with
per-key Map fallback. An unconstrained `Any` leaf retains generic recursive
equality, and an unconstrained `Any` root compared with a container retains the
generic root plan. Stored-value representability and JSONB metadata
requirements remain in force; consistency between stored values and sidecar
metadata is the caller's responsibility. The same schema-guided recursive
equality is available to `in()`, `contains()`, and `unique()` when reachable
List/Map/Struct alternatives are bounded and representation checks succeed.
An `Any` comparison root remains generic, as does a nested `Any` leaf. These
membership paths do not remove normalization that may happen earlier on the
whole receiver. See the [measured root comparison](../../optimizations/007-postgresql-guided-json-equality.md),
[bounded root equality](../../optimizations/024-postgresql-container-union-equality.md),
and [bounded membership comparison](../../optimizations/027-postgresql-schema-membership-equality.md)
records for their distinct scopes and fixtures.

For example, with `value`, `needle`, and list elements declared as
`String|List<Int>|List<Float>`, these queries use the membership operations
while retaining the declared alternatives:

```hff
value:in(needle)
items:contains(needle)
items:unique
```

When the columns use JSONB, provide numeric type metadata wherever the stored
representation cannot otherwise recover the declared numeric kind.

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
receiver or one of these methods' string arguments has a dynamic `Any` or union
representation. A runtime string is extracted and passed to the ordinary
string operation; a non-string runtime value becomes ordinary HFF null. This
works both for a dynamic receiver and for a known string receiver with a
dynamic JSON argument. It also dispatches `duration()` on a dynamic JSON
receiver when its runtime value is a string; see
[computed durations](#parse-computed-durations).

```hff
data:len=5
data:startswith("Al")
data:endswith("ice")
name:startswith(data)
(data:duration):type="duration"
(data:len):type="null"
```

Here `data` can be an `Any` JSON value and `name` a schema-known string.
When reached during evaluation, a no-value receiver or argument remains
no-value. For methods with an argument, an ordinary null receiver or a
non-string dynamic receiver produces null and suppresses a later no-value
argument under the normal receiver-first evaluation rule. A non-string dynamic
argument produces null when the receiver is a string. `contains()` also has
dynamic String/List dispatch, described below. The computed Duration rules are
described [above](#parse-computed-durations). Dynamic `concat()` is described
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
Bytes, timestamps, and durations are not inferred inside `Any`; a schema-known
Bytes value is supported as described above. An `Any` list or
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
  `number()`, `duration()`, `fromjson()`, `frombase64()`, `frombase64r()`,
  `startswith()`, `endswith()`,
  String/List `contains()`, and String/List `concat()` described above.
- Other dynamic operations whose result cannot be resolved from schema
  candidates, a supported type guard, or a statically typed string/numeric
  index.
- Dynamic folds whose seed or callback-result representations cannot be
  determined from the schema and available JSONB metadata.
- Dynamic `Any` versus Bytes comparisons and generic dynamic tree comparisons
  involving Bytes; dynamic JSON representations remain JSON-native. Other
  multi-non-null unions containing Bytes remain unsupported where their
  representations overlap or the operation cannot be lowered. `String|Bytes`
  cannot be recovered from JSON strings. The schema-known
  `Bytes|Int|Float|Null`, `Bytes|Bool`, `Bytes|Bool|Null`, and
  `Bytes|Bool|Int|Float|Null` subsets are described in the Bytes section.
  Schema-known Bytes values in JSON arrays, objects, and callback-produced
  collections are supported; `frombase64()` can also produce a scalar Bytes
  value.
- Timestamp zone arguments, data-dependent string parsing, and ambiguous
  string-backed Timestamp unions remain unsupported for JSON/JSONB; native
  `timestamptz` columns use the separate `BindTimestamp` binding.
- Native Duration columns use `bigint` nanoseconds. Schema-known JSON/JSONB
  Duration values use canonical duration strings; `Any` strings remain Strings.
  The `Duration|Int|Null`, `Duration|Bool|Null`, `Duration|Int|Bool|Null`, and
  `Duration|Float|Null` subsets support the operations listed above. Broader
  dynamic dispatch across tagged unions remains unsupported when the active
  type cannot be recovered or the requested operation cannot be lowered.
- JSONB float64 serialization is unsupported when storage has lost numeric
  representation needed to produce the result. More generally, operations
  requiring a runtime type not fixed by schema or metadata remain unsupported.

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

For a fixed literal field chain, the translator can restore numeric metadata
from only the selected subtree when the path is rooted at a direct metadata-
backed JSONB binding, crosses a flattened `BindStruct`, or reads a JSONB
document from a relation item's physical binding. Parent presence/null state
is preserved, and metadata lookup continues to use pointers relative to the
original document. Whole-document access and paths that cross a guard,
computed receiver, or dynamic index use the general restoration path. A fixed
field prefix extracted before a later guard can still qualify. Callback scopes
remain isolated; a fixed path rooted at a relation item's bound JSON document
can qualify inside that item's scope. This is a SQL-shape optimization, not
validation of stored values or metadata; the caller-owned consistency contract
is unchanged. See the
[PostgreSQL translation choices](optimizations.md#restore-only-a-selected-jsonb-metadata-subtree)
and [measured flattened/relation cases](../../optimizations/010-postgresql-structured-metadata-subtrees.md).

The recursive restoration walk also avoids expanding scalar leaves as
children. It still visits each leaf needed to restore numeric representations,
apply the document-relative metadata pointer, and reconstruct the selected or
complete value. Other flattened and relation paths still restore their full
document. This walk does not validate stored data; the caller remains
responsible for consistency between JSON and metadata. See
[JSONB leaf expansion](../../optimizations/005-postgresql-jsonb-leaf-expansion.md)
for the leaf-filter measurements and limitations.

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
