# PostgreSQL Backend

The SQL backend turns a compiled HFF program into a reusable PostgreSQL
predicate and ordered parameters. It does not open a database connection or
execute SQL. The service owns the surrounding statement, connection, and
execution timeout.

## Build a predicate

This example maps two schema fields to trusted database identifiers:

```go
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.Nullable(schema.String("name")),
        schema.Nullable(schema.Int("age")),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `age>=18.5 name:contains("api")`)
if err != nil {
    return err
}

query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(
        hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
        hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
    ),
)
if err != nil {
    return err
}

rows, err := db.QueryContext(ctx,
    `SELECT records.id FROM records WHERE `+query.Where(), query.Args()...)
```

`Path` and `Column` take identifier components, not SQL fragments. A schema
field literally named `service.name` is bound with `hff.Path{"service.name"}`;
nested fields use multiple path components. Each column component is quoted
separately. Every referenced field must have a binding. A path can pass through
required struct fields, but not through an optional or nullable parent because
a flattened column cannot preserve the parent's missing/null state.

You can also pass `hffsql.WithConfig(&hffsql.Config{Dialect: hffsql.PostgreSQL,
Bindings: bindings})`; functional options apply from left to right, so a later
dialect or binding option replaces the earlier value for that setting.

The backend emits typed parameters for HFF literals and SQL `NULL` for literal
null. `Where()` returns the predicate fragment; `Args()` returns a fresh slice
in placeholder order. The `Query` owns its data and can be reused concurrently.
The predicate text is an implementation artifact, not a stable formatting
contract. Compose it into a statement owned by the service and pass the
parameters to the database API; do not interpolate query values into SQL text.

## Supported PostgreSQL subset

In addition to scalar columns, the backend supports an initial schema-typed
JSON/JSONB profile. See [PostgreSQL JSON and JSONB bindings](json.md) for its
binding API, supported field and collection access, storage requirements, and
current rejection boundary.

The initial builder supports nullable scalar columns whose HFF type is one of
null, boolean, int64, finite float64, or string. Required struct parents may be
traversed to a scalar leaf. Comparisons support homogeneous scalar types and
mixed int64/float64 ordering without rounding the integer through float64;
string ordering uses PostgreSQL's `C` collation. Boolean literals and operators,
implicit AND, explicit null comparisons, numeric unary negation, postfix guards
at scalar leaves, scalar type guards, `exists()`, and `type()` are supported.
String methods currently supported are `len()`, `contains()`, `startswith()`,
`endswith()`, and `concat()`; boolean `not()`, `and()`, and `or()` are supported.

Integer arithmetic supports `+`, `-`, `*`, `%`, and integer/integer `/`:

```text
age+1>18
age/2>10.5
age%2=0
```

Integer addition, subtraction, multiplication, negation, and remainder preserve
HFF's int64 range and signed-remainder behavior. Intermediate arithmetic is
checked before conversion back to `bigint`; overflow produces null. Division
returns float64, and a zero divisor produces null. For example, when `age` is
the maximum int64 value, `(age+1):exists` is false because the overflowing
result is null. Float/float and mixed int/float arithmetic remain unsupported
and are rejected by `Build`.

For mixed numeric ordering, the builder partitions float values around the
int64 range, floors only in the safely castable range, and compares the integer
with that bigint value before considering the fractional part. It never converts
the int64 operand to float64, which would lose precision near 2^53 and above.

Mapped database columns must use schema-compatible PostgreSQL types: `bigint`,
`double precision`, `boolean`, or `text`. Float values must be finite. Text must
be valid UTF-8 without NUL. Identifier components must contain 1–63 UTF-8 bytes
and no NUL; PostgreSQL identifiers that would be truncated are rejected.
The database must use UTF-8 encoding for HFF string ordering with `C` collation
to agree with HFF's Unicode codepoint ordering. `Build` does not inspect the
database schema or data: the service must ensure the bound columns have the
declared HFF types and nullability, and that stored values meet these encoding
and finiteness constraints.

An unsupported operation or type returns a source-located `hff.BackendError`
from `Build`. For scalar bindings, the current subset rejects regex, collection
operations, `Any` and non-null mixed unions, JSON and temporal conversions,
float/float and mixed int/float arithmetic, and guarded access through nullable
parents. JSON bindings support the schema-known access and inspection described
in the [JSON guide](json.md); dynamic `Any`, mixed non-null unions, callbacks,
and representations whose runtime HFF type cannot be recovered remain
unsupported there. JSON list/object structural equality is available for
schema-known values when its capability is enabled; transforming callbacks,
collection transformations, and JSON conversions remain unsupported.
Unsupported operations do not fall back to local evaluation.
PostgreSQL is the only currently supported SQL dialect.

The builder uses relational subqueries for chained operations to keep generated
SQL growth linear. This may make the predicate shape more involved; callers
should treat it as opaque and rely on the bound parameters, not its exact text.

## Add a PostgreSQL translator

An extension declaration is shared with the engine, while a SQL translator is
registered separately for a dialect. The following translator lowers a
service-specific string method to PostgreSQL `lower`:

```go
declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
    ID: "service.withsuffix.v1", Name: "withsuffix",
    Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1,
})
if err != nil { return err }

e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.String("name"))),
    engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))),
    engine.WithMethods(declaration),
)
if err != nil { return err }
program, err := e.Compile(ctx, `name:withsuffix="Alice!"`)
if err != nil { return err }

translator := hffsql.Implement(declaration, hffsql.PostgreSQL,
    func(call *hffsql.Call, receiver hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
    suffix, err := call.Parameter(hff.String("!"))
    if err != nil { return hffsql.Expression{}, err }
    return call.Expression(schema.StringType, "{} || {}", receiver, suffix)
    },
)
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))),
    hffsql.WithMethods(translator),
)
if err != nil { return err }
```

`Call.Expression` accepts a trusted developer-authored template and replaces
each `{}` with a scoped expression. `Call.Parameter(value)` creates a bound
scalar parameter using the dialect's validated encoding. Never place query
text, record values, or `$N` placeholders in a template; use `Parameter` for
values. HFF checks that expressions belong to the current call and that the
returned expression has the declared scalar type, but it does not validate the
template's SQL semantics. Keep templates under developer control and preserve
the declaration's behavior on its supported domain.

HFF continues to own method signature selection, capability checks, null and
no-value handling, and argument evaluation. A translator runs while `Build`
constructs the query; it is not called for each record and must be safe for
concurrent builds. It must return an error when it cannot faithfully translate
the operation. SQL currently supports only the PostgreSQL dialect and scalar
expressions; a registered translator does not make unsupported collection or
composite types available.

## Inspect and request SQL execution guarantees

`ExecutionGuarantees(dialect)` and `Query.Guarantees()` report whether the
selected backend can enforce HFF's per-record `MaxSteps` and
`MaxGeneratedValues` budgets. PostgreSQL currently reports neither guarantee.
An explicit positive request is rejected by `Build` with a backend error:

```go
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithExecutionLimits(hffsql.ExecutionLimits{MaxSteps: 10_000}),
)
```

Zero means no HFF execution-budget guarantee was requested; negative values are
configuration errors. These settings are not database timeouts. Configure
database timeouts and cancellation through the database API used by the
service. Program-local `MaxSteps` and `MaxGeneratedValues` do not transfer to
SQL execution.

## Execution and limits

`Build` is a pure translation step. The SQL backend does not enforce local
`MaxSteps` or `MaxGeneratedValues` during database execution, and SQL evaluation
does not promise HFF's local short-circuit work order. Do not treat local runtime
budgets as SQL execution guarantees. The service controls database cancellation
and timeouts through its own database API and context. The current SQL
`Config` contains only a dialect and bindings; the Build options do not express
a SQL runtime-budget requirement. Program-local runtime limits are not SQL
requests and are not copied into the predicate. If a service needs a database
execution guarantee, it must enforce it through its database integration. A
future API must reject an explicit constraint request that the selected backend
cannot enforce rather than silently ignore it.

## Run the example

The example only builds a predicate; it does not connect to PostgreSQL:

```sh
go run ./examples/sql-predicate
go run ./examples/sql-extensions
```

Expected output:

```text
Predicate built: true
Parameter count: 2
```

The extension example adds a translator that appends a bound `!` suffix and
prints:

```text
Predicate built: true
Parameter count: 2
PostgreSQL MaxSteps guarantee: false
```

For a step-by-step extension walkthrough, see the [PostgreSQL translator
tutorial](../../tutorials/04-postgresql-translator.md).

## Test against PostgreSQL

The tagged integration tests execute scalar parity cases against a running
PostgreSQL container using `docker exec` and `psql`. Set
`HFF_POSTGRES_CONTAINER` to the name or ID of a running container with the
standard `postgres` user and `psql` installed, then run:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -tags=integration ./backends/sql
```

Without the environment variable, the integration test fails instead of
silently skipping the database check. The ordinary SQL package tests remain
database-independent.
