# Capabilities and Resource Limits

## Configuration options

```go
base := &engine.Config{
    Schema: schema.Struct(schema.String("name")),
    Capabilities: capabilities.Standard(),
}
e, err := engine.New(
    engine.WithConfig(base),
    engine.WithSchema(serviceSchema),
)
```

Options apply left to right; the last assignment to a setting wins. WithConfig
replaces the whole configuration at its position, including zero values. New
validates the final configuration and retains no mutable Config pointer. A nil
Config or a zero Option is a configuration error.

## Select capabilities explicitly

```go
features := capabilities.Standard().Without(capabilities.Regex)
e, err := engine.New(
    engine.WithSchema(serviceSchema),
    engine.WithCapabilities(features),
)
```

An omitted set is an error; None() is an explicitly empty set. Allow and Without
return new sets. Has tests permission; IDs returns sorted, copied IDs. Standard
contains the built-in language feature and method catalog, rather than promising
that every backend implements every member. Query capability checks and backend
support checks are applied when those stages are implemented.

| ID constant | Meaning |
|:--|:--|
| Logical | Boolean operators and implicit AND |
| Comparison | Equality and ordering comparisons |
| Arithmetic | Numeric arithmetic |
| Access | Field access |
| Index | List and object indexing |
| Guard | Postfix path guards |
| Collections | List and object literals |
| Callbacks | Callback expressions |
| RegexOperators | Regex operators |
| Regex | Group selecting RegexOperators and string.matches |

Method("string.len") selects a built-in method by receiver family and name.
For example:

```go
features := capabilities.None().Allow(
    capabilities.Access,
    capabilities.Callbacks,
    capabilities.Method("list.any"),
)
```

The list methods any, all, map, flatmap, foldl, and foldr require Callbacks.
Missing dependencies are errors, not implicitly enabled features. The Regex
group adds or removes both regex operators and string.matches. Unknown IDs are
rejected. Custom method declaration/implementation registration is not yet
implemented, so custom IDs are not accepted by the current engine.

## Limits

```go
e, err := engine.New(
    engine.WithSchema(serviceSchema),
    engine.WithCapabilities(features),
    engine.WithLimits(hff.Limits{
        MaxInputDepth: 32,
        MaxInputValues: 20_000,
    }),
)
```

Zero selects the default for that item. Negative values are errors.
WithUnlimitedLimits explicitly disables ceilings; a subsequent WithLimits
restores finite defaults and applies its overrides. Config.UnlimitedLimits
provides the same explicit choice when assembling configuration as data.

| Limit | Initial default | Counting contract |
|:--|--:|:--|
| MaxInputDepth | 64 | Root depth is zero; each value nested in a container adds one |
| MaxInputValues | 100,000 | Every value, including root and containers, counts once |
| MaxQueryBytes | 65,536 | UTF-8 source bytes; reserved for compilation |
| MaxSyntaxDepth | 64 | Syntax nesting; reserved for compilation |
| MaxSteps | 1,000,000 | Evaluation work; reserved for the local runtime |
| MaxGeneratedValues | 100,000 | Generated values; reserved for the local runtime |

Only input limits are enforced by the current implementation. Compilation and
runtime counters will be specified and enforced with those stages. DefaultLimits
returns a value copy. Limits returns the engine's resolved configuration; zero
ceilings in an explicitly unlimited engine indicate that opt-out.

The initial input benchmarks exercise records containing 1, 1,000, and 10,000
integer list elements. Run them on your target hardware:

```sh
go test ./engine -run '^$' -bench BenchmarkValidateRecord -benchmem
```

On an Apple M3 Max with Go 1.27.1, a local 100 ms benchmark sample measured
approximately 0.18 µs, 11.6 µs, and 114 µs respectively, with zero allocations
per successful validation in these list benchmarks. Constructor work is outside
the measured loop. This sample does not predict another schema, hardware,
extension, or backend's performance.

These are configurable starting ceilings, not performance guarantees or limits
on the storage already allocated by the caller. Validation checks context and
counts all nested values, including Any contents.

## Handle errors

```go
var detail *hff.Error
if errors.As(err, &detail) {
    switch detail.Code {
    case hff.InputError:
        // detail.Path identifies the rejected value.
    case hff.LimitError:
        // Validation exceeded its configured ceiling.
    case hff.CancelledError:
        // The context was cancelled or its deadline expired.
    case hff.ConfigurationError:
        // Correct the service configuration.
    }
}
```

Errors retain context causes for errors.Is(err, context.Canceled) and
errors.Is(err, context.DeadlineExceeded). Input paths contain literal names;
list positions appear as decimal indices. Values are not included in diagnostics.
Completed evaluation results and ordinary null are separate from these errors.
