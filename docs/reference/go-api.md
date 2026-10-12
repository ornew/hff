# Go API Reference

This page lists the public construction path and stable package entry points.
See [pkg.go.dev](https://pkg.go.dev/github.com/ornew/hff) for the complete
exported Go documentation and the accepted [API design record](../design/004-go-api.md)
for rationale. Query semantics remain defined by the
[language specification](../../spec/README.md).

## Main packages

| Package | Purpose |
|:--|:--|
| `github.com/ornew/hff` | Immutable values, types, limits, errors, and compiled-plan inspection. |
| `github.com/ornew/hff/schema` | Struct, map, and primitive schema constructors. |
| `github.com/ornew/hff/adapters/govalue` | Optional schema-checked conversion from Go values. |
| `github.com/ornew/hff/capabilities` | Standard and custom capability sets. |
| `github.com/ornew/hff/engine` | Engine configuration, validation, and query compilation. |
| `github.com/ornew/hff/backends/local` | Reusable local filters and matching. |
| `github.com/ornew/hff/backends/sql` | Pure SQL translation and parameterized artifacts. |

## Configure and compile

Configure the schema and capability policy once, then compile query strings to
immutable programs:

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
```

`engine.Engine` is safe for concurrent validation and compilation. A
`hff.Program` is immutable and can be passed to supported backend builders.
Compilation returns typed `*hff.Error` diagnostics; inspect its `Code` and
source `Span` rather than branching on message text.

## Construct values and validate input

The explicit value API avoids reflection and conversions:

```go
record := hff.Object(map[string]hff.Value{
    "name": hff.String("Alice"),
    "age":  hff.Int(18),
})
input, err := e.Validate(ctx, record)
if err != nil {
    return err
}
```

The `schema` package provides constructors such as `schema.String`,
`schema.Int`, `schema.Bool`, `schema.Number`, `schema.List`, `schema.Map`, and
`schema.Struct`. `hff.Value` and `hff.Type` are immutable. Use `Value` accessors
such as `Kind`, `AsString`, `AsInt`, `Lookup`, `At`, and `Equal` to inspect
them. `Input.Value()` returns the validated immutable value and
`Input.Compatible(type)` checks exact schema compatibility.

For optional reflection-based Go-to-HFF conversion, use
[`adapters/govalue`](../guides/go-values.md). It requires an explicit HFF
schema and explicit mappings for Go structs; it does not replace the explicit
`Value` API or infer schema from Go types.

## Build and run a local filter

```go
filter, err := local.Build(ctx, program)
if err != nil {
    return err
}
matched, err := filter.Match(ctx, record)
// Or pass a previously validated Input to MatchInput.
```

The filter is immutable and safe for concurrent matches. `Match` validates its
record; `MatchInput` checks schema compatibility and reuses validated input.
Local runtime limits can be set with `local.WithConfig`, `local.WithLimits`, or
`local.WithUnlimitedLimits`. Inspect typed errors with `errors.As` into
`*hff.Error`.

## Build a SQL artifact

SQL construction also accepts a `hff.Program` and performs no database I/O:

```go
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(
        hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
        hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
    ),
)
```

Inspect `query.Where`, `query.Args`, and `query.Guarantees`. Their accessors
return copies. The caller owns database connections, execution, transactions,
and timeouts. Consult the [PostgreSQL backend reference](../backends/postgresql/README.md)
for supported operations and constraints.

To bind a schema-known value stored in a native JSON or JSONB column, use
`hffsql.BindJSON` with an explicit format:

```go
hffsql.BindJSON(
    hff.Path{"profile"},
    hffsql.Column("records", "profile"),
    hffsql.JSONB,
)
```

See the [PostgreSQL JSON binding guide](../backends/postgresql/json.md) for the
supported schema profile, collection access and equality behavior, and storage
requirements.

For a native PostgreSQL composite column, bind an unambiguous struct schema
with `hffsql.BindComposite(path, column)`. Composite attribute names must match
the schema field names; attributes are always present, so a null attribute is
an explicit null rather than an absent optional field. See the
[composite binding guide](../backends/postgresql/composites.md) for the current
physical type profile and its limits.

## Inspect a compatibility profile

Profiles provide immutable snapshots of known backend and build facts. The
static local profile is available from `local.Profile()`; a built filter reports
its effective configuration through `filter.Profile()`. For SQL, use
`hffsql.Profile(dialect)` for the static translation profile and
`query.Profile()` for the built query's profile. SQL profile inspection does
not connect to or inspect a database.

```go
profile := filter.Profile()
if support, known := profile.Feature(capabilities.Method("timestamp.year")); known {
	// support.Level is Supported, Conditional, or Unsupported.
	// Conditional entries include the condition in support.Conditions.
}

if limits, known := profile.RuntimeLimits(); known {
	// A known zero field means explicitly unbounded for that stage.
	_ = limits.MaxSteps
}

if version, known := profile.Version("tz"); known {
	_ = version // Supplied zone-data version, when configured.
}
```

`Profile` exposes `Backend`, `Features`, `Feature`, `Capabilities`,
`CompileLimits`, `InputLimits`, `RuntimeLimits`, `Versions`, `Version`,
`Guarantees`, `HasGuarantee`, `Details`, and `Methods`. Slice accessors return
copies. A missing feature or version is unknown; `UnknownSupport` is not
reported as supported. Each stage-limit accessor returns `(Limits, bool)`: a
false boolean means unknown or outside that backend's responsibility, while a
true boolean with a zero limit field means the ceiling is explicitly
unbounded. The capability set reports service policy only when specified.
Backend feature support is a separate statement and does not grant permission.
Registered method declarations in `Methods()` likewise do not enable those
methods.

Profiles do not infer database, collation, Unicode, regex, or time-zone data
versions from ambient state. A missing version is unknown. Guarantees list only
behavior the backend reports; absence is not a promise. In particular, a SQL
profile does not claim that PostgreSQL enforces local evaluation budgets or
HFF evaluation order. `Build` remains authoritative for a specific program,
binding, and configuration, and database execution behavior remains under the
service's control.

Run the [profile example](https://github.com/ornew/hff/blob/main/examples/profiles/main.go)
from the repository root with `go run ./examples/profiles` to inspect both a
built local filter and a translated PostgreSQL query.

## Related lookup pages

- [Built-in method signatures](methods.md)
- [Program and Node inspection](program-ir.md)
- [Specification](../../spec/README.md)
- [Backend options and support matrices](../backends/README.md)
