# Local Backend Configuration and Limits

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
that every backend implements every member. Common query compilation checks
enabled capabilities; each backend builder separately checks support for the
program operations it implements.

| ID constant | Meaning |
|:--|:--|
| Logical | Boolean operators and implicit AND |
| Comparison | Equality and ordering comparisons |
| Arithmetic | Numeric arithmetic |
| Access | Field access |
| Index | List and object indexing |
| Guard | Postfix path guards |
| TypeGuard | Explicit type guards (`value:[Type]`) |
| Collections | List and object literals |
| Callbacks | Callback expressions |
| RegexOperators | Regex operators |
| Regex | Group selecting RegexOperators and string.matches |
| ListEquality | Structural equality of list values |
| ObjectEquality | Structural equality of object values |
| StructuralEquality | Group selecting ListEquality and ObjectEquality |

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
rejected. A declared extension uses `capabilities.Method(declaration.ID())` and
must also be included in `engine.WithMethods`. The engine checks that every
capability ID is built in or belongs to a configured declaration. See the
[local extension guide](extensions.md) for the complete flow.

Structural equality permissions are checked during compilation for `=`/`!=`,
`in()`, `list:contains()`, and `list:unique()`. Scalar comparisons and explicit
null tests do not need them. Required permissions follow the statically
possible compared types, recursively through compatible nested containers;
explicit type guards narrow those types. The StructuralEquality group enables
both list and object equality. Removing the group removes both permissions;
each permission can then be selected independently.

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
| MaxJSONDepth | 64 | JSON root is depth one; nested arrays and objects add one per level |
| MaxQueryBytes | 65,536 | UTF-8 source bytes per compilation |
| MaxSyntaxDepth | 64 | Syntax nesting per compilation |
| MaxCallbackDepth | 64 | Nested callback depth per compilation; the outermost callback is depth 1 |
| MaxCompileSteps | 1,000,000 | Semantic analysis visits per compilation, including repeated callback inference |
| MaxSteps | 1,000,000 | Evaluated nodes and traversed data per local match |
| MaxGeneratedValues | 100,000 | Generated collection containers and entries per local match |

Compilation enforces query-byte, syntax-depth, callback-depth, and semantic-step
limits; JSON parsing and JSON serialization enforce `MaxJSONDepth`; input
validation enforces input-depth and input-value limits; the local backend
enforces per-match step and generated-value limits. Runtime `MaxSteps`
also counts values traversed by equality and type guards and the lengths
processed by string and byte operations. `MaxGeneratedValues` counts collection
containers and entries. SQL backends report the execution constraints they can
enforce; configure database timeouts through the database integration. A
requested constraint that the selected backend cannot enforce must be rejected.
DefaultLimits returns a value copy. Limits returns the engine's resolved
configuration; zero ceilings in an explicitly unlimited engine indicate that
opt-out.

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
counts all nested values, including Any contents. It checks UTF-8 in host
strings and object keys in chunks of at most 4,096 bytes, adjusted to avoid
splitting a rune, and checks context between chunks. Cancellation during a
large scan returns a cancellation error with the context status and any
host-supplied cause preserved; validation never returns a completed partial
input. Map keys are still visited in byte-lexical order, matching Go's
`sort.Strings`; the cancellable ordering checks context while collecting keys,
merging runs, and comparing long common prefixes. Cancellation returns no
partially ordered key list.

Local JSON parsing also checks context while scanning whitespace, quoted
strings and object keys, and unquoted tokens, with context checks at
approximately 4 KiB intervals. Scanning a two-byte JSON escape may cross a
checkpoint by one byte. The original JSON input bytes are charged once;
checkpoints do not charge them again. Cancellation returns an error with the
context cause preserved and no partial value. The initial whole-input UTF-8
validity check and the standard-library JSON string decoder are single calls
that cannot be interrupted internally; HFF checks context after they return.
This is cooperative cancellation, not immediate preemption during every
decoding step.

## Handle errors

```go
var detail *hff.Error
if errors.As(err, &detail) {
    switch detail.Code {
    case hff.InputError:
        // detail.Path identifies the rejected value.
    case hff.BackendError:
        // The selected backend does not support an operation in the program.
    case hff.LimitError:
        // Validation exceeded its configured ceiling.
    case hff.CancelledError:
        // The context was cancelled or its deadline expired.
    case hff.ConfigurationError:
        // Correct the service configuration.
    }
}
```

Cancellation returns a `CancelledError` while preserving both the context
status and its cause in the error chain. `errors.Is` matches
`context.Canceled` or `context.DeadlineExceeded`; for contexts created with
`context.WithCancelCause` or `context.WithDeadlineCause`, it also matches the
host-supplied cause. Input paths contain literal names; list positions appear
as decimal indices. Values are not included in diagnostics.
Completed evaluation results and ordinary null are separate from these errors.
