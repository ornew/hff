# Local Backend

Compile a query once, build a reusable local filter, and match records against
the schema used by the engine. A filter is immutable and safe for concurrent
calls. Each match uses fresh evaluation state.

```go
import (
    "context"
    "errors"
    "fmt"

    "github.com/ornew/hff"
    "github.com/ornew/hff/backends/local"
    "github.com/ornew/hff/capabilities"
    "github.com/ornew/hff/engine"
    "github.com/ornew/hff/schema"
)
```

## Compile, build, and match

```go
ctx := context.Background()
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

filter, err := local.Build(ctx, program)
if err != nil {
    return err
}

record := hff.Object(map[string]hff.Value{
    "name": hff.String("Alice"),
    "age":  hff.Int(20),
})
matched, err := filter.Match(ctx, record)
if err != nil {
    return err
}
fmt.Println(matched) // true
```

`Match` validates the supplied value against the program's input schema on each
call, traversing the whole schema-shaped record even if the query reads only one
scalar field, then evaluates it. `false, nil` means evaluation completed and
the record did not match. Validation failures, cancellation, and exceeded
runtime budgets return errors instead of a match result. Ordinary null and
internal no-value follow the language's boolean-conversion rules.

## Reuse validated input

For repeated matches, validate the record once and use `MatchInput`:

```go
input, err := e.Validate(ctx, record)
if err != nil {
    return err
}

matched, err := filter.MatchInput(ctx, input)
if err != nil {
    return err
}
```

`Input` is immutable. `MatchInput` checks that its schema contract exactly
matches the program and skips record validation. The contract compares field
types, required/optional presence, nullability, numeric subtypes, and missing
value behavior; it does not require the same schema object or capability set.
An invalid or incompatible input returns an input error without a completed
match. For repeated searches, validate once at ingestion and retain the
immutable `Input`; this avoids traversing fields that an individual query does
not read.

## Configure evaluation limits

`local.Build` accepts backend-specific functional options. Individual limits
can override the defaults:

```go
filter, err := local.Build(ctx, program,
    local.WithLimits(hff.Limits{
        MaxSteps:           200_000,
        MaxGeneratedValues: 20_000,
    }),
)
```

`WithConfig`, `WithLimits`, and `WithUnlimitedLimits` apply in order; a later
option replaces or overrides earlier settings. `WithConfig` takes a `*local.Config`
with `Limits` and `UnlimitedLimits` fields. Config values are copied during
build. For ordinary limits, zero selects the documented finite default and a
negative value is invalid. Unlimited mode must be selected explicitly.

`MaxSteps` counts evaluated nodes, values traversed during equality and type
guards, and string/byte operation lengths. `MaxGeneratedValues` counts generated
collection containers and entries. Exhausting either budget returns a limit
error; HFF does not return a partial match. Context cancellation is checked
during evaluation. These budgets apply to each match call independently.

## Supported operations

The local evaluator supports core boolean and arithmetic operators, equality
and ordering, regex operators, truthiness, null and no-value propagation,
postfix guards and explicit type guards, indexing, collection literals, and
logical short-circuiting. It supports every built-in method family in the
[method catalog](../../reference/methods.md):

| Receiver family | Methods |
|:--|:--|
| Any | `type()`, `tojson()`, `tostring()`, `in(any...)`, `exists()` |
| Bool | `and(bool)`, `or(bool)`, `not()` |
| Number | `round()`, `ceil()`, `floor()` |
| String and bytes | Text, JSON/base64, timestamp/duration conversions, and byte length |
| List | Size, index presence, membership, uniqueness, sort, concat, callbacks, and folds |
| Object | Size, key presence, get, keys, values, and items |
| Timestamp and duration | Parsing, comparison, arithmetic, components, and formatting |

For exact parameter and result types, consult the
[method reference](../../reference/methods.md). The backend
supports the scalar and string examples in the
[scalar/string guide](../../guides/scalar-methods.md) and list/object methods in
the [collections guide](../../guides/collections.md) and
[callback guide](../../guides/callbacks.md). JSON conversions are covered in
the [JSON guide](../../guides/json.md). Local custom methods are supported; see
[Local extension methods](extensions.md) for declaration, capability,
evaluator, and metering requirements. Base64 conversions are covered in the
[scalar/string guide](../../guides/scalar-methods.md). A program containing a method or
operation not supported by the local backend fails during `Build` with
`hff.BackendError`. Build never silently drops an operation. Use the
[normative method specification](../../../spec/methods.md) for query semantics;
backend support is reported separately from compile validity.

Local regex matching follows HFF's RE2 reference syntax except that `\C` is
excluded and is treated as an invalid pattern, producing null. Other backends
may provide a smaller regex subset; check their support matrix or compatibility
profile for documented restrictions.

## Distinguish errors from non-matches

Use `errors.As` to inspect structured HFF errors:

```go
var detail *hff.Error
if errors.As(err, &detail) {
    switch detail.Code {
    case hff.BackendError:
        // The local backend cannot execute an operation in this program.
    case hff.InputError:
        // The record or validated input does not satisfy the input contract.
    case hff.LimitError:
        // A configured per-match budget was exceeded.
    case hff.CancelledError:
        // errors.Is matches Canceled/DeadlineExceeded and any host cause
        // supplied with context.WithCancelCause/WithDeadlineCause.
    }
}
```

`BackendError` is raised while building an unsupported program. Runtime errors
mean that matching did not complete; a completed `false` result is not an
error.

Run the complete example from the repository root:

```sh
go run ./examples/local
```

It prints `Match: true` and `MatchInput: true`.
