# PostgreSQL Composite Bindings

`hffsql.BindComposite` maps a schema-known HFF struct to a native PostgreSQL
composite column. It preserves row-null state, attribute nulls, nested structs,
and supported arrays while lowering the value into the backend's typed access
representation. `Build` does not connect to PostgreSQL or inspect types or rows;
the service must create compatible composite types and store values that match
the declared HFF schema.

## Define and bind a composite column

Use a struct schema whose field names match the PostgreSQL composite attribute
names. This example binds a nullable profile that contains a list of nullable
person composites:

```go
personType := schema.Struct(schema.Nullable(schema.String("name")))
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.Nullable(schema.Field("profile", schema.Struct(
            schema.Nullable(schema.Int("age")),
            schema.Nullable(schema.List("people", schema.NullableType(personType))),
        ))),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `profile!.age>=18 profile.people:any(person=>person!.name="Alice")`)
if err != nil {
    return err
}

query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindComposite(
        hff.Path{"profile"},
        hffsql.Column("records", "profile"),
    )),
)
if err != nil {
    return err
}
rows, err := db.QueryContext(ctx,
    `SELECT records.id FROM records WHERE `+query.Where(), query.Args()...)
```

The corresponding PostgreSQL types might be declared as:

```sql
CREATE TYPE hff_person AS (name text);
CREATE TYPE hff_profile AS (age bigint, people hff_person[]);
CREATE TABLE records (id bigint, profile hff_profile);
```

Insert a SQL-null parent and a non-null composite parent to see the physical
distinction:

```sql
INSERT INTO records VALUES
    (1, NULL),
    (2, ROW(42, ARRAY[ROW('Alice')::hff_person, NULL::hff_person])::hff_profile),
    (3, ROW(NULL, NULL)::hff_profile),
    (4, ROW(0, ARRAY[]::hff_person[])::hff_profile);
```

For row 2, the reconstructed HFF value is
`{"profile":{"age":42,"people":[{"name":"Alice"},null]}}`; row 3 has a
present profile with both attributes null, and row 4 has a present profile
with an empty `people` list. Row 1 has a null profile. The filter above selects
row 2; rows 1, 3, and 4 do not match. The `profile` field in
this example is required but nullable, as declared in the schema, so it cannot
represent a missing profile. If the HFF field is optional, a native composite
still cannot distinguish absence from a present composite; use `BindStruct` or
another representation with an explicit presence channel.

The [runnable composite example](https://github.com/ornew/hff/tree/main/examples/sql-composite)
builds this predicate without database I/O:

```sh
go run ./examples/sql-composite
```

It prints `Predicate built: true` and `Parameter count: 11`.

The path identifies the HFF value, while `Column` contains trusted PostgreSQL
identifier components, not SQL source. A composite binding must target an
unambiguous struct schema. It cannot overlap another binding for a descendant
path or conflict with another physical format for the same column.

## Presence, null, and supported attributes

A SQL `NULL` composite is an HFF null struct. A non-null row whose attributes
are all null remains an object containing those explicit null values. Nested
composites follow the same rule. PostgreSQL composite attributes always exist;
a null attribute is an explicit HFF null, never an absent optional field. A
composite therefore cannot represent absent optional fields. If the HFF schema
marks a field optional, use this binding only when the stored rows guarantee
that the attribute is present.

Supported leaf values are null, bool, int64, finite float64, and string.
Nested struct attributes and one-dimensional PostgreSQL arrays of supported
scalars or structs map to HFF structs and lists; nullable elements retain their
null values. Array elements retain logical order even when the PostgreSQL
array has a non-default lower bound. A SQL `NULL` array is HFF null; an empty
array is an empty list. Array rank is a caller storage precondition: `Build`
does not inspect row data to verify one-dimensionality.

Maps, dynamic `Any`, ambiguous non-null unions, bytes, timestamps, durations,
nested-list schemas, and unsupported physical representations are rejected by
`Build`. The application must ensure that composite attribute names and types,
array element types and dimensions, nullability, and actual row values satisfy
the schema. In particular, arrays must be one-dimensional. `Build` does not
introspect PostgreSQL declarations or validate stored rows.

## Selecting fields

A plain field path such as `profile.age` or `profile.father.name` reads the
corresponding native composite attributes directly. It does not construct JSON
for unrelated attributes or arrays. SQL `NULL` at the composite parent and a
null selected attribute retain their HFF null behavior. When a query needs the
whole composite value, or uses a guarded, dynamic, or method-based access, the
backend uses the general typed conversion path; that path converts the full
schema-known value. These are SQL-shape choices only and do not promise a
particular execution-time improvement.

## Execution and verification

Composite translation is part of pure SQL construction. The returned predicate
and ordered parameters are consumed by the caller's database API; the SQL
backend does not execute queries, own connections, or set database timeouts.
See the [PostgreSQL backend overview](README.md) for execution and limit
contracts.

The tagged PostgreSQL integration suite checks composite rows against local HFF
evaluation, including SQL-null rows, all-null rows, nested composites, arrays
of nullable scalar and composite elements, null attributes, and non-default
array lower bounds. The composite matrix covers 49 queries across six records
(294 query/record pairs); the broader SQL matrix also covers 5,556 JSON/JSONB,
5,680 scalar, and 152 extension parity pairs on PostgreSQL 18.6:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -tags=integration ./backends/sql
```

See [Development checks](../../development/checks.md) for the test environment.
