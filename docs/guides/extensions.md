# Extension Methods

An extension adds a typed receiver method to the common compiler and supplies a
local evaluator separately. Declarations are immutable and shared by the
engine; implementations belong to a backend. Extensions cannot replace built-in
methods or run arbitrary query-supplied Go code.

## Declare one signature

Create a declaration before constructing the engine:

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

`ID` is the stable identity for this signature and its capability. `Name` is the
ASCII receiver-method name written in HFF. `Receiver`, `Parameters`, and
`Result` use immutable `hff.Type` descriptors. `Parameters` are required;
`Optional` adds trailing optional parameter types and `Variadic` declares a
repeated trailing type. A zero `Variadic` means there is no variadic parameter.
Optional types specify accepted argument types; they do not provide default
values. Optional arguments are positional and cannot be skipped to supply an
argument for the variadic tail.

Every signature has its own ID. Different IDs may share a `Name` when their
receiver and parameter types do not overlap. `engine.New` rejects duplicate IDs,
built-in name/ID collisions, and overlapping signatures, then retains the
declarations immutably. `NewMethodDeclaration` copies the supplied type slices.
`MethodDeclaration` provides `Valid()`, `ID()`, `Name()`, `Receiver()`,
`Parameters()`, `Optional()`, `Variadic()`, `Parameter(i)`, `Result()`,
`MinArgs()`, `MaxArgs()`, `NullPolicy()`, `BaseCost()`, and `Equal(other)`.
`Parameters()` and `Optional()` return copies; `MaxArgs()` returns `-1` for a
variadic signature. `Engine.Methods()` also returns a copied slice of immutable
declarations.

`BaseCost` must be at least one. `NullPolicy` defaults to
`hff.PropagateNull`. With `hff.InvokeNull`, the evaluator may be called when the
receiver or an argument is null only if that position's declared type admits
null. Internal no-value always propagates and is never exposed as `hff.Value`.

## Grant the capability and compile

Declaring a method does not enable it. Select its ID explicitly through
`capabilities.Method`:

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

The compiler checks method name, arity, receiver/argument compatibility, and
capability permission. For a call that can select multiple declared signatures,
every statically applicable signature must be enabled. A type guard can narrow
a dynamic receiver before this check. `Engine.Methods()` exposes the configured
declarations for service configuration and discovery.

## Register the local evaluator

The local backend requires an implementation for every used extension
signature. Unused declarations need no evaluator. Build fails with a
source-located `hff.BackendError` when a used declaration is missing or the
implementation does not match its declaration.

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

The evaluator is a pure, concurrent-safe Go function. Its receiver and
arguments have already been checked against the selected declaration. It may
return `hff.Null(), nil` for an ordinary data-dependent failure. A non-null
result must be a valid HFF value matching the declared result type; invalid
values, non-finite floats, invalid UTF-8, or a wrong result type produce a
backend error.

HFF charges the fixed base cost before calling the evaluator. Additional
data-dependent work must be metered with `Call.ChargeSteps`; all variable-size
results and generated values must be charged with `Call.ChargeGeneratedValues`,
including results that reuse immutable input storage. Return a charging error
immediately. Charging
errors are latched and still abort the match if the evaluator ignores them.
`Call.Context()` provides the operation context; check it during long-running
work because the backend cannot preempt arbitrary evaluator code. Context
cancellation and limit exhaustion abort evaluation with their original HFF
error codes; ordinary evaluator errors and recovered panics become
`hff.BackendError`. HFF physically validates the returned value, charges
validation traversal/string work, applies `MaxInputDepth`, and checks the
declared result type. A `Call` is active only during its invocation and must
not be retained for later work.

## Nulls and no-value

With the default `PropagateNull`, a null receiver or argument returns null
without invoking the evaluator. If null leaves no viable signature, later
arguments are not evaluated. `InvokeNull` opts into receiving null, subject to
nullable declared types. No-value from a failed guard or another absent
subexpression always propagates without invoking an extension; it cannot be
constructed or returned as a public value. Top-level null and no-value both
produce a false filter result after HFF's ordinary boolean conversion.

The SQL backend does not yet support extension translators. A SQL `Build`
therefore rejects a program that uses a custom method, even if a local
implementation is registered. See the [SQL guide](sql.md) for its current
support matrix.

## Run the example

```sh
go run ./examples/extensions
```

Expected output:

```text
Alice: true
Bob: false
```
