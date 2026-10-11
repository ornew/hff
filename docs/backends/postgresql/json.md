# PostgreSQL JSON and JSONB Bindings

The PostgreSQL backend can bind a schema-known HFF value to one native JSON or
JSONB column. `Build` lowers field access, list indexing, selected collection
inspection, structural equality, predicates, and type guards into a
parameterized predicate. It does not connect to PostgreSQL, inspect the table
schema, or validate stored documents.

## Bind one JSON document

Declare the JSON document's HFF shape in the engine schema. A single non-null
type family may also be nullable; dynamic `Any` and unions with multiple
non-null families cannot be mapped faithfully from a JSON column.

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
`BindJSON(path, column, format)`. The path identifies the schema value stored
in that column. Its nested struct fields and list/map contents are accessed
inside the JSON document. Do not bind both a JSON ancestor and one of its
descendant paths, and do not map two schema paths to one physical column using
conflicting formats; `Build` rejects these conflicting mappings.

The query values remain bound parameters. `Where()` and `Args()` are the
database-facing artifact; the application owns execution, connections,
transactions, and timeouts. See the [PostgreSQL backend guide](README.md) for
the general construction contract.

## Supported profile

The schema must determine each JSON value's HFF representation. Supported
families are null, bool, int64, finite float64, string, homogeneous lists,
string-keyed maps, and structs. Nullable variants of one family are supported.
JSON numeric values are interpreted according to the declared HFF schema; the
application must store values that satisfy that schema. `Build` does not
inspect or validate the JSON documents in database rows.

Known-schema field access is supported for nested struct/map fields. List
indexing accepts negative indexes from the end and integer-valued floating
indexes. A fractional or out-of-range index produces a null result. Missing
object keys can be checked with `has()`; `get()` returns null for a missing
key, so use `has()` when that distinction matters. JSON null remains a value
and propagates through the ordinary HFF evaluation rules.

For schema-known JSON objects, the backend supports `size()`, `has(key)`,
`get(key)`, `keys()`, `values()`, and `items()`. Enumeration orders keys with
PostgreSQL's `C` collation. For schema-known JSON lists, it supports `size()`,
`has(index)`, `contains()`, `unique()`, `any()`, and `all()`. `contains()`
requires every supplied needle to be present; a null needle is an ordinary
value, while a null list receiver propagates null. A no-value argument
propagates even if an earlier needle matched. `unique()` compares each element
by HFF equality, so repeated stored nulls and repeated nested composite values
are duplicates. These operations require applicable list/object equality
capabilities for the types actually compared.

Nested callbacks can capture outer callback values and schema fields, with
lexical shadowing:

```hff
orders:any(order=>order.items:any(item=>item.price>100))
```

Inside those callbacks, scalar membership is supported as well:

```hff
profile.roles:any(role=>role:in("admin","editor"))
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
profile.scores:any(score=>score:in(80,90))
```

The following operations remain unsupported for JSON-backed values and return
a source-located `hff.BackendError` from `Build`:

- Dynamic `Any` decoding and unions with multiple non-null families when the
  stored representation cannot determine the HFF type.
- Transforming callbacks such as `map()`, `flatmap()`, `foldl()`, and `foldr()`;
  `sort()` and other unsupported collection transformations.
- JSON parsing/serialization methods, conversions to bytes/timestamps/durations,
  and operations requiring a runtime type not fixed by the schema.

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

JSONB normalizes some input spelling, including numeric exponent notation and
the distinction between integer and floating spellings. This profile uses the
explicit HFF schema to select numeric types when the schema determines them;
ambiguous nested int/float unions and dynamic `Any` remain unsupported without
type metadata. Conversions or operations that need discarded JSON spelling or
signed-zero details are rejected. Keep the application schema authoritative
for the bound column.

PostgreSQL is the only supported SQL dialect. JSON and JSONB use their native
operators; this support does not promise stable generated SQL text or database
execution order. SQL execution constraints are reported by the SQL API, and
the service remains responsible for database timeouts and cancellation.

## Verification

The repository's tagged integration suite compares JSON and JSONB bindings
against local evaluation on PostgreSQL 18.6. The verified matrix covers 2,640
JSON/JSONB query-record pairs for scalar and nested access, null and missing-key
cases, list indexing, object/list inspection and equality, callbacks, type
guards, and type/truthiness predicates.
To run it against a PostgreSQL
container with `psql` installed:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -tags=integration ./backends/sql
```

See [development checks](../../development/checks.md) for the test environment.
Run the [JSON binding example source](https://github.com/ornew/hff/tree/main/examples/sql-json)
with `go run ./examples/sql-json`.
