# Add a PostgreSQL method translator

This tutorial adds a small service-specific method and translates it into a
parameterized PostgreSQL predicate. It builds the SQL artifact only; the
service remains responsible for connecting to PostgreSQL and executing it.

## Declare and enable the method

Each extension signature has its own stable ID. Add the declaration to engine
configuration and select its capability explicitly:

```go
declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
    ID: "service.withsuffix.v1",
    Name: "withsuffix",
    Receiver: schema.StringType,
    Result: schema.StringType,
    BaseCost: 1,
})
if err != nil {
    return err
}

features := capabilities.Standard().Allow(capabilities.Method(declaration.ID()))
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.String("name"))),
    engine.WithCapabilities(features),
    engine.WithMethods(declaration),
)
if err != nil {
    return err
}
program, err := e.Compile(ctx, `name:withsuffix="Alice!"`)
if err != nil {
    return err
}
```

Compilation validates the HFF declaration and capability. Backend support is a
separate check performed by `sql.Build`.

## Translate to a typed SQL expression

Register a PostgreSQL translator for the same declaration:

```go
translator := hffsql.Implement(declaration, hffsql.PostgreSQL,
    func(call *hffsql.Call, receiver hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
        suffix, err := call.Parameter(hff.String("!"))
        if err != nil {
            return hffsql.Expression{}, err
        }
        return call.Expression(schema.StringType, "{} || {}", receiver, suffix)
    },
)
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))),
    hffsql.WithMethods(translator),
)
if err != nil {
    return err
}
```

The template is trusted code owned by the service. Each `{}` is replaced with
an expression scoped to the current translator call. Use
`call.Parameter(hff.Value)` to bind values; do not interpolate query text or
record data into SQL templates. HFF checks expression ownership and declared
scalar types, but the service remains responsible for the template's semantic
correctness. If the translator cannot preserve the method's behavior, return
an error so `Build` reports a source-located backend failure.

HFF retains signature selection, capability enforcement, null/no-value
handling, and argument evaluation. The translator runs during pure query
construction, not once per database row. Translators must be concurrency-safe
for concurrent builds and must not retain the call after returning.

## Check backend guarantees

Ask what the dialect can enforce before requesting execution limits:

```go
guarantees, err := hffsql.ExecutionGuarantees(hffsql.PostgreSQL)
if err != nil {
    return err
}
fmt.Println(guarantees.MaxSteps, guarantees.MaxGeneratedValues)
```

PostgreSQL currently reports `false` for both limits. A positive request through
`WithExecutionLimits` therefore fails `Build` instead of being ignored. These
HFF budgets are not database timeouts; configure database cancellation and
timeouts through the database driver and context.

## Run the complete example

From the repository root, run:

```sh
go run ./examples/sql-extensions
```

The example prints whether a predicate was built, its parameter count, and the
PostgreSQL budget guarantees. It never opens a database connection. For the
complete current scalar support matrix and parameter-mapping rules, see the
[PostgreSQL backend guide](../backends/postgresql/README.md).

Expected output:

```text
Predicate built: true
Parameter count: 2
PostgreSQL MaxSteps guarantee: false
```
