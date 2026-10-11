# Compile a Query

Use Go 1.27.1 to compile query source against an engine's schema, capabilities,
and limits. `Engine.Compile` returns an immutable, backend-independent
`hff.Program`; it does not evaluate records or build a local or SQL backend.

## Configure and compile

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

program, err := e.Compile(ctx, `name="Alice" age>=18`)
if err != nil {
    return err
}
_ = program // Reuse this typed program with a supported backend when available.
```

Compilation parses the complete query, resolves schema references and method
signatures, checks types and enabled capabilities, and enforces the rule that a
filter must depend on service data. The method registry includes configured
extension declarations. Local implementations are registered when building a
filter; see the [extension guide](extensions.md). See [the program guide](program-ir.md)
for the immutable IR and its source spans.

## Handle diagnostics

Compilation failures are returned as `*hff.Error`. Use `errors.As` to inspect
the error code and source location without depending on diagnostic text:

```go
program, err := e.Compile(ctx, source)
if err != nil {
    var detail *hff.Error
    if errors.As(err, &detail) {
        switch detail.Code {
        case hff.SyntaxError:
            // Fix the query syntax.
        case hff.TypeError:
            // Check schema references and expression types.
        case hff.CapabilityError:
            // Enable all applicable operations or narrow the value explicitly.
        case hff.LimitError:
            // Adjust the configured budget or simplify the query.
        case hff.CancelledError:
            // The context was cancelled; errors.Is also matches its cause.
        }
        if detail.Span != nil {
            fmt.Printf("query error at %d:%d: %s\n",
                detail.Line, detail.Column, detail.Message)
        }
    }
    return err
}
```

`Span` is a half-open UTF-8 byte range in the original query. `Line` and
`Column` provide a human-readable location. Error wording may evolve; branch on
the stable error code instead.

## Dynamic values and capabilities

For an `Any` or union receiver, compilation requires permission for every
statically applicable method signature. A capability-disabled candidate is a
compile-time error even if the runtime value might select a different family.
An explicit type guard can narrow the receiver before the capability check:

```hff
value:[string]:contains("x")
```

The guard preserves a matching value and yields internal no-value on mismatch;
it does not convert the value. A `T|null` receiver resolves methods from `T`
and propagates null normally. A receiver known only as null has no family to
select, so all statically applicable candidates must be enabled. See the
[language type guide](../../spec/types.md) for complete rules.

## Compilation budgets

Compilation enforces `MaxQueryBytes`, `MaxSyntaxDepth`, `MaxCallbackDepth`, and
`MaxCompileSteps`. These bound UTF-8 source size, syntax nesting, nested callback
depth, and semantic analysis visits (including repeated callback-inference
visits). Exceeding a limit returns a limit error. These are compile-time limits;
they do not bound query execution. Local execution uses the separate per-match
`MaxSteps` and `MaxGeneratedValues` budgets. SQL backends report which execution
constraints they can enforce; configure database timeouts through the database
integration. A requested unsupported constraint must be rejected. Cancellation
is checked during compilation. Each operation uses the engine's configured
finite limits unless unlimited mode was explicitly selected.

Compilation produces a typed plan only. The local backend checks support and
requires implementations for used extension methods when building a filter; see
the [local evaluation guide](local-evaluation.md) for supported operations and
limits. SQL extensions require separate dialect-specific translators; see the
[PostgreSQL guide](sql.md) for typed expressions and execution guarantees.

Run the complete example from the repository root:

```sh
go run ./examples/compilation
```
