# Use a Filter in a Service

A service should compile a saved or user-authored filter when it is created or
updated, then reuse the resulting local filter for requests. This example also
validates records once at ingestion and calls `MatchInput` when searching them.

## Compile when the filter changes

Keep the schema under application control. In this example, each ticket has a
team and active flag. Compile and build the query once:

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
filter, err := local.Build(ctx, program)
if err != nil {
    return err
}
```

Treat a compile or build error as a rejected filter configuration. Do not store
or run a filter that failed either step. HFF queries select values from the
service schema; they do not add fields or execute Go functions.

## Validate records at ingestion

Convert trusted application data to explicit HFF values and validate it before
storing it for later searches:

```go
record := hff.Object(map[string]hff.Value{
    "team":   hff.String("support"),
    "active": hff.Bool(true),
})
input, err := e.Validate(ctx, record)
if err != nil {
    return err
}
```

`Input` is immutable. Keep the validated input alongside the service record or
in a cache whose invalidation follows the record's lifecycle.

## Reuse validated inputs

Each request can now evaluate the same filter without repeating input
validation:

```go
matched, err := filter.MatchInput(ctx, input)
if err != nil {
    return err
}
```

`MatchInput` still checks that the input's schema contract matches the filter.
If records were not validated at ingestion, use `Match` with the original HFF
value instead. Both operations use fresh per-call state and return errors for
cancellation and exceeded evaluation limits. See [Build and run a local
filter](../guides/local-evaluation.md) for limit configuration and error codes.

## Reject invalid query changes

Compilation errors are structured HFF errors. An application can report a
source location to the filter author while keeping the active filter unchanged:

```go
program, err := e.Compile(ctx, source)
if err != nil {
    var detail *hff.Error
    if errors.As(err, &detail) {
        return fmt.Errorf("filter rejected at %d:%d: %w",
            detail.Line, detail.Column, err)
    }
    return err
}
```

Only replace the active filter after both compilation and `local.Build` succeed.
Use stable error codes for application behavior rather than matching diagnostic
text; see [Compile a query](../guides/compiling.md).

## Run the complete example

From the repository root:

```sh
go run ./examples/tutorial-service
```

It prints:

```text
ticket-17: true
ticket-18: false
```
