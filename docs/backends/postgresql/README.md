# PostgreSQL Backend

The SQL backend turns a compiled HFF program into a reusable PostgreSQL
predicate and ordered parameters. It does not open a database connection or
execute SQL. The service owns the surrounding statement, connection, and
execution timeout.

The context passed to `Build` cancels SQL translation only. A `CancelledError`
matches `context.Canceled` or `context.DeadlineExceeded` through `errors.Is`;
for contexts created with `context.WithCancelCause` or
`context.WithDeadlineCause`, it also matches the host-supplied cause. Canceling
database execution requires the context passed to the database API.

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

`hff.Path` names fields in the logical HFF record; `Column` names components of
the physical PostgreSQL identifier. They are different namespaces. A field
literally named `service.name` is `hff.Path{"service.name"}`; a nested field
uses `hff.Path{"service", "name"}`. `Column("people", "name")` means the
quoted identifier `"people"."name"`, not SQL text. A dot inside one component
is part of that identifier. Every field the query can read must have a
binding. `Bind` can traverse required struct fields, but cannot represent a
missing or null parent. A JSON object can represent a missing key; a flattened
struct needs explicit parent-state predicates. A composite distinguishes a
SQL-null row from a present row, but its attributes cannot be missing. A
relation is always a list and needs explicit state predicates only when
missing or null list states are required.

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

Choose a physical binding from the way the application stores each HFF value:

| Physical storage | Binding | What it maps |
| --- | --- | --- |
| Scalar SQL column | `Bind(path, Column(...))` | One HFF scalar field to one column. SQL `NULL` is an explicit HFF null. |
| JSON/JSONB column | `BindJSON(path, column, JSON/JSONB)` | One complete schema-known HFF object, map, or list value. Descendant paths are read inside that document. JSONB may add `WithTypeMetadata(metadataColumn)` for numeric types lost by normalization. |
| Native composite column | `BindComposite(path, column)` | One HFF struct to a PostgreSQL composite. A SQL-null row is null; its attributes are always present. |
| Struct spread across columns | `BindStruct(path, present, null, fields...)` | One HFF object with caller-supplied parent state and per-field bindings. |
| Related rows | `BindRelation(path, relation)` | An HFF list whose items come from one caller-selected correlated row source. |

These bindings are not interchangeable. A one-to-one value can be a composite
or a flattened struct; `BindRelation` always represents a list, even if the
database currently has at most one matching row. A one-to-many relation maps
one parent key to zero or more rows. Nested one-to-many values use a nested
`BindRelation` for a field of the item type. The current relation source is
one table or view plus a caller-authored `SQLPredicate` correlation: HFF does not infer
foreign keys, generate joins, or discover many-to-many paths. To expose a
many-to-many result, provide a database view that presents the desired rows or
bind the junction relation and a nested relation explicitly.

The [binding tutorial](../../tutorials/06-postgresql-bindings.md) walks through
a parent table and child rows. The representation-specific guides explain
each binding's missing/null/empty rules.

In addition to scalar columns, the backend supports a schema-typed JSON/JSONB
profile. JSONB bindings can use a separate version-1 JSON Pointer sidecar to
restore supported numeric representations. See [PostgreSQL JSON and JSONB
bindings](json.md) for the binding API, metadata format, collection access,
storage requirements, and current rejection boundary.
Struct values can also be bound to native PostgreSQL composite columns with
`BindComposite`; see [composite bindings](composites.md) for attribute presence,
null handling, and the supported nested and array profile.
For schemas spread across columns, use [flattened struct bindings](flattened-structures.md);
for caller-owned correlated row sources, see [relation bindings](relations.md).
See [PostgreSQL translation optimizations](optimizations.md) for SQL-shape
choices, semantic boundaries, and reproducible translation and database
measurements.

The initial builder supports nullable scalar columns whose HFF type is one of
null, boolean, int64, finite float64, or string. Required struct parents may be
traversed to a scalar leaf. Comparisons support homogeneous scalar types and
mixed int64/float64 ordering without rounding the integer through float64;
string ordering uses PostgreSQL's `C` collation. Boolean literals and operators,
implicit AND, explicit null comparisons, numeric unary negation, postfix guards
at scalar leaves, scalar type guards, `exists()`, and `type()` are supported.
The generic `json()` and `string()` methods support the scalar and
collection profile described in the JSON guide, including finite float values
from scalar columns and native JSON. JSONB float serialization requires the
numeric metadata described in that guide. String and recoverable dynamic JSON
values support `bool()` and `number()`; `number()` requires PostgreSQL 16 or
later. They also support `fromjson()` on PostgreSQL 16 and later; see the JSON
guide for parsing rules and its NUL precondition.
String methods currently supported are `len()`, `contains()`, `startswith()`,
`endswith()`, and `concat()`; boolean `not()`, `and()`, and `or()` are supported.
The generic scalar `in()` method is supported for membership among its
arguments. For JSON-backed lists, the backend also supports `contains()`,
`unique()`, `sort()`, `concat(list)`, `any()`, `all()`, `map()`, and
`flatmap()`; callbacks may use scalar `in()`. See the
[JSON guide](json.md) for equality capabilities and collection semantics.

Integer and floating-point arithmetic follow HFF's numeric rules:

```text
age+1>18
age/2>10.5
age%2=0
price*1.08>=100.0
count+rate>10.0
```

Integer addition, subtraction, multiplication, negation, and remainder preserve
HFF's int64 range and signed-remainder behavior. Intermediate arithmetic is
checked before conversion back to `bigint`; overflow produces null. Division
returns float64, and a zero divisor produces null. For example, when `age` is
the maximum int64 value, `(age+1):exists` is false because the overflowing
result is null.

Float/float and mixed int/float `+`, `-`, `*`, and `/` are supported. Mixed
arithmetic promotes the integer operand to float64. Results use IEEE 754
round-to-nearest, ties-to-even; overflow or a non-finite result yields null,
while subnormal results and underflow to signed zero remain valid. Float
remainder is unsupported. PostgreSQL lowering preserves these results without
relying on the database's floating-point arithmetic for final rounding; see
the [numeric JSON guide](json.md#numeric-arithmetic) and
[design record 015](../../design/015-postgresql-float-arithmetic.md).

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
operations, `Any` and non-null mixed unions, conversions outside the JSON
guide's `bool()`, `number()`, and `fromjson()` profile, and guarded access
through nullable parents.
JSON bindings support the
schema-known access and inspection described in the [JSON guide](json.md).
JSONB metadata supports selected numeric type
guards and typed indexing; unguarded dynamic comparisons and methods, and
ambiguous representations that schema and metadata cannot recover, remain
unsupported. JSON list/object structural equality, list
`contains()` and `unique()`, `sort()`, `concat(list)`, and `any()`/`all()` are
available for schema-known values when their required capabilities are enabled;
`map()` and `flatmap()` are supported when callback result types are known.
Folds are supported for known accumulator types; see the JSON guide for their
type and null rules. Other collection transformations and conversions to
bytes/timestamps/durations, float64 serialization when JSONB
has lost required numeric representation, and other unsupported types remain
unsupported.
Unsupported operations do not fall back to local evaluation.
PostgreSQL is the only currently supported SQL dialect.

The builder uses relational subqueries for chained operations to keep generated
SQL growth linear. This may make the predicate shape more involved; callers
should treat it as opaque and rely on the bound parameters, not its exact text.
Literal nodes can refer directly to their typed bound parameters without an
extra one-row projection. Values remain in `Args()` rather than interpolated
into SQL text; computed expressions and guards retain their `OFFSET 0`
projection barriers.
When a step's presence is statically known to be true, the translator can retain
that constant instead of projecting a separate presence column. Presence that
depends on null or type guards remains explicit, as do value projections, so
this simplification preserves null and no-value behavior. It is a conservative
SQL-shape optimization; no performance improvement is claimed.
For explicitly bound scalar-column paths and schema-backed JSON, JSONB, and
composite values, each projection scope has its own path cache. Repeated
unguarded schema-known accesses can share projections within that scope,
including callback parameter field reads keyed by binding identity, field path,
and type. Captures, shadowed bindings, and separate callbacks do not share
entries across scopes. Guarded paths, dynamic indexes, and method calls are not
cache entries. This preserves scope and presence behavior while changing SQL
shape only; it assumes bound rows honor the declared HFF schema, and no
measured performance improvement is claimed.
The declared schema also permits limited check elimination: a non-nullable
child does not need a redundant SQL null test, and a type guard needs no second
type check when its constraint exactly matches the child's inferred type.
Nullable, `Any`, and differently constrained values keep their checks. These
rules assume stored rows honor the declared schema; `Build` does not inspect or
validate database rows, and presence remains distinct from null/no-value.

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

## Inspect the PostgreSQL profile

`hffsql.Profile(hffsql.PostgreSQL)` reports the translator's declared support
without connecting to a database. After a successful `Build`, `query.Profile()`
also includes the program's effective capability policy, compile limits, and
registered SQL method declarations. Neither profile inspects the database or
infers its version, collation, Unicode, regex, or time-zone data versions.

```go
profile, err := hffsql.Profile(hffsql.PostgreSQL)
if err != nil {
	return err
}
if support, known := profile.Feature(capabilities.RegexOperators); known {
	fmt.Println(support.Level, support.Conditions)
}

queryProfile := query.Profile()
if limits, known := queryProfile.CompileLimits(); known {
	fmt.Println(limits.MaxCompileSteps)
}
if _, known := queryProfile.RuntimeLimits(); !known {
	fmt.Println("SQL runtime limits are not reported")
}
```

Feature levels describe translator support independently of service
permissions. A conditional feature includes its condition; a missing feature
is unknown. The built query's capability set reports service policy, and
registered declarations in `Methods()` do not grant that policy. SQL profiles
do not report input limits, runtime limits, or positive evaluation guarantees:
the database owns row validation and SQL execution, and PostgreSQL does not
enforce the local evaluation budgets or evaluation order.

The [profile example](https://github.com/ornew/hff/blob/main/examples/profiles/main.go)
also shows that a translated query reports unknown SQL runtime limits rather
than inheriting the local evaluator's budgets.

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
go run ./examples/sql-json-metadata
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
