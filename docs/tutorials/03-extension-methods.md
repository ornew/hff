# Add a Service-Specific Method

This tutorial adds a pure `hasprefix` method for string fields, selects its
capability, compiles a human-authored query, and registers a local evaluator.
The full declaration and metering reference is in the
[local extension guide](../backends/local/extensions.md).

These snippets belong in a Go program using the following imports:

```go
import (
    "context"
    "fmt"
    "strings"

    "github.com/ornew/hff"
    "github.com/ornew/hff/backends/local"
    "github.com/ornew/hff/capabilities"
    "github.com/ornew/hff/engine"
    "github.com/ornew/hff/schema"
)
```

At the start of `main`, create the operation context:

```go
ctx := context.Background()
```

## Declare the method

The method takes one string receiver and one required string argument and
returns a boolean. Its stable ID is separate from the name people type in a
query:

```go
declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
    ID:         "service.string.hasprefix",
    Name:       "hasprefix",
    Receiver:   schema.StringType,
    Parameters: []hff.Type{schema.StringType},
    Result:     schema.BoolType,
    BaseCost:   1,
})
if err != nil {
    return err
}
```

The declaration describes the language contract. It contains no Go function;
the engine and local backend receive their own configuration.

## Enable and compile it

```go
features := capabilities.Standard().Allow(capabilities.Method(declaration.ID()))
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.String("name"))),
    engine.WithCapabilities(features),
    engine.WithMethods(declaration),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `name:hasprefix("Al")`)
if err != nil {
    return err
}
```

Capability permission is explicit. Compilation checks the call's types and
rejects a query if the extension signature is not selected.

## Implement it for local matching

The evaluator uses HFF values, meters its input-dependent work, and returns a
boolean:

```go
filter, err := local.Build(ctx, program, local.WithMethods(local.Implement(
    declaration,
    func(call *local.Call, receiver hff.Value, args []hff.Value) (hff.Value, error) {
        name, _ := receiver.AsString()
        prefix, _ := args[0].AsString()
        if err := call.ChargeSteps(len(name)); err != nil {
            return hff.Null(), err
        }
        return hff.Bool(strings.HasPrefix(name, prefix)), nil
    },
)))
if err != nil {
    return err
}
```

The declaration's fixed base cost is charged automatically. The evaluator
charges extra work through `Call`; it must be pure and safe for concurrent
matches. Missing implementations and invalid result values fail explicitly.
Null propagates by default, and internal no-value never reaches the evaluator.

## Match records

```go
for _, name := range []string{"Alice", "Bob"} {
    matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
        "name": hff.String(name),
    }))
    if err != nil {
        return err
    }
    fmt.Printf("%s: %t\n", name, matched)
}
```

The output is `Alice: true` and `Bob: false`. The complete runnable program is
in [`examples/extensions/main.go`](https://github.com/ornew/hff/blob/main/examples/extensions/main.go):

```sh
go run ./examples/extensions
```

For overloads, optional/variadic parameters, explicit null invocation, and
metering details, continue with the [local extension guide](../backends/local/extensions.md).
