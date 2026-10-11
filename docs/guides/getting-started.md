# Configure an Engine and Validate Records

This tutorial uses the implemented configuration and input APIs. HFF separates
service configuration, query compilation, and backend building. This first step
establishes the service's schema and validates the records it will supply.

## Create the engine

Use Go 1.27.1 and import the packages:

```go
import (
    "context"
    "fmt"

    "github.com/ornew/hff"
    "github.com/ornew/hff/capabilities"
    "github.com/ornew/hff/engine"
    "github.com/ornew/hff/schema"
)
```

Declare the record's fields and explicitly select a feature set:

```go
e, err := engine.New(
    engine.WithSchema(schema.Struct(
        schema.String("name"),
        schema.Int("age"),
    )),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}
```

The root schema is a struct or string-keyed map. New checks the schema, feature
selection, and limits. The engine snapshots its configuration and supports
concurrent validation calls.

## Supply a record

Construct explicit HFF values:

```go
record := hff.Object(map[string]hff.Value{
    "name": hff.String("Alice"),
    "age": hff.Int(18),
})
input, err := e.Validate(context.Background(), record)
if err != nil {
    return err
}
name, _ := input.Value().Lookup("name")
text, _ := name.AsString()
fmt.Println(text)
```

This prints `Alice`. Validate checks the complete record, including dynamic Any
values, then returns immutable validated input. It does not insert absent keys
or convert int64 values into float64 values. A missing required non-nullable
field, unknown struct field, invalid value, or incompatible type is an input
error. Use a map schema when keys are data-dependent.

## Reuse the input contract

```go
otherSchema := schema.Struct(schema.Int("age"), schema.String("name"))
fmt.Println(input.Compatible(otherSchema)) // true
```

Field declaration order does not affect compatibility. Field types, optional
presence, and nullability do. Changing only the engine's feature selection does
not change the input contract. Validated input can be reused by a local filter
through `MatchInput`. Continue with the [compilation guide](compiling.md) and
[local evaluation guide](local-evaluation.md) to build and run a query.

## Run the complete example

From a repository checkout:

```sh
go run ./examples/configuration
```

The example prints `Alice true`. See [schemas and values](schemas-and-values.md)
for nested records and [configuration](configuration.md) for feature policies,
limits, and error handling.
