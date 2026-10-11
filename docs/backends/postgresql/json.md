# PostgreSQL JSON and JSONB Bindings

The PostgreSQL backend can bind a schema-known HFF value to one native JSON or
JSONB column. `Build` lowers field access, list indexing, selected collection
inspection, predicates, and type guards into a parameterized predicate. It
does not connect to PostgreSQL, inspect the table schema, or validate stored
documents.

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
PostgreSQL's `C` collation. For schema-known JSON lists, it supports `size()`
and `has(index)`. Truthiness, `exists()`, `type()`, scalar comparison, and
structural type guards are available when their types are known from the
schema. The backend rejects an operation during `Build` when the JSON storage
type cannot preserve the required HFF semantics.

The following operations remain unsupported for JSON-backed values and return
a source-located `hff.BackendError` from `Build`:

- Dynamic `Any` decoding and unions with multiple non-null families.
- Collection callbacks, structural equality, `contains()`, `unique()`,
  `sort()`, and other collection transformations.
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
the distinction between integer and floating spellings. This initial profile
uses the explicit HFF schema to select the numeric type for supported typed
access. It rejects dynamic `Any`, mixed non-null unions, and conversions or
operations that would need to recover discarded JSON spelling or signed-zero
details. Keep the application schema authoritative for the bound column.

PostgreSQL is the only supported SQL dialect. JSON and JSONB use their native
operators; this support does not promise stable generated SQL text or database
execution order. SQL execution constraints are reported by the SQL API, and
the service remains responsible for database timeouts and cancellation.

## Verification

The repository's tagged integration suite compares JSON and JSONB bindings
against local evaluation on PostgreSQL 18.6. The current verified matrix has
980 query/record pairs and covers scalar and nested
access, null and missing-key cases, list indexing, object/list inspection,
type guards, and type/truthiness predicates. To run it against a PostgreSQL
container with `psql` installed:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -tags=integration ./backends/sql
```

See [development checks](../../development/checks.md) for the test environment.
Run the [JSON binding example source](https://github.com/ornew/hff/tree/main/examples/sql-json)
with `go run ./examples/sql-json`.
