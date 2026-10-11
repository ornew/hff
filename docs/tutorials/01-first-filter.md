# Write Your First Filter

This tutorial takes a query written by a person and evaluates it against
service records. The application owns the schema and runtime; the query only
chooses which records match.

## 1. Describe the record

The example service tracks support tickets. Its filterable fields are a team
name and an active flag:

```go
schema.Struct(
    schema.String("team"),
    schema.Bool("active"),
)
```

An explicit schema lets HFF reject misspelled fields and incompatible
operations before the service evaluates any records.

## 2. Compile a human-authored query

The filter might come from a search box or saved view:

```hff
team="support" active=true
```

Spaces between predicates mean AND. Configure the engine with the schema and
the capabilities your service intends to expose, then compile the source:

```go
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.String("team"),
        schema.Bool("active"),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `team="support" active=true`)
if err != nil {
    return err
}
```

Compilation checks syntax, field names, types, capabilities, and query limits.
It does not evaluate a record. See [Compile a query](../guides/compiling.md)
for structured diagnostics and the distinction between compilation and
backend building.

## 3. Build and match locally

Build a reusable local filter from the typed program. Then pass records as
immutable HFF values:

```go
filter, err := local.Build(ctx, program)
if err != nil {
    return err
}

record := hff.Object(map[string]hff.Value{
    "team":   hff.String("support"),
    "active": hff.Bool(true),
})
matched, err := filter.Match(ctx, record)
if err != nil {
    return err
}
fmt.Println(matched) // true
```

`Match` validates the record against the program's input schema and evaluates
the filter. A completed non-match is `false, nil`; invalid input, cancellation,
or an exhausted runtime budget is an error.

## Run the complete example

From the repository root:

```sh
go run ./examples/tutorial-first-filter
```

It prints:

```text
ticket-17: true
ticket-18: false
```

Continue with [Use a filter in a service](02-service-integration.md) to move
compilation out of the request path and reuse validated records.
