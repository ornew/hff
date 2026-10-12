# Converting Go Values

The optional `adapters/govalue` package converts ordinary Go records into
owned, schema-checked HFF values. Choose an explicit schema and field mapping
when the HFF contract is independent of Go structs, or infer a schema and typed
filter from a Go type with `Binding[T]`.

The examples use these imports:

```go
import (
	"context"
	"fmt"
	"reflect"

	"github.com/ornew/hff/adapters/govalue"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)
```

## Configure a converter

Map HFF object keys to direct exported Go struct fields. The mapping direction
is HFF field name to Go field name:

```go
type Person struct {
	Name string
	Age  int64
}

personType := schema.Struct(
	schema.String("name"),
	schema.Int("age"),
)
converter, err := govalue.New(
	govalue.WithSchema(personType),
	govalue.WithStructs(govalue.StructMapping{
		Type: reflect.TypeFor[Person](),
		Fields: map[string]string{
			"name": "Name",
			"age":  "Age",
		},
	}),
)
if err != nil {
	return err
}
```

`WithSchema` is required. Struct mappings are required for every Go struct type
that conversion encounters; mappings are exact by `reflect.Type`. Only direct,
exported fields are eligible. Unmapped fields are omitted. Go tags, embedded
field promotion, and implicit case conversion are not used. A string-keyed map
can supply an object without a struct mapping; named string key types are
accepted.

## Convert and evaluate

`Convert` returns an owned `hff.Value`, which can be validated or passed to a
backend like any other explicit value:

```go
ctx := context.Background()
record, err := converter.Convert(ctx, Person{Name: "Alice", Age: 42})
if err != nil {
	return err
}

e, err := engine.New(
	engine.WithSchema(personType),
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
matched, err := filter.Match(ctx, record)
if err != nil {
	return err
}
fmt.Println(matched)
```

The converter validates its result against the configured schema before it
returns. `Match` validates again as part of its normal input contract; when a
caller already has an `hff.Input`, use `MatchInput` to reuse that validated
input.

## Mapping rules

Conversion preserves supported Go representations rather than coercing between
HFF types. Boolean and string values keep their kinds; signed integers and
representable unsigned integers become HFF integers; finite `float32` and
`float64` values become HFF floats. Integer values are not promoted to floats,
and floats are not truncated to integers. Unsigned integers larger than
`math.MaxInt64`, NaN, and infinities are rejected.

`[]byte` becomes HFF bytes. Other slices and arrays become lists. Maps with
string-kind keys and mapped structs become objects. `time.Time` becomes a
timestamp and `time.Duration` becomes a duration; the converter does not parse
time strings. Nil maps, slices, pointers, and interfaces become explicit null.
Non-nil empty maps and slices remain empty values of their mapped kinds,
including empty bytes for a non-nil empty `[]byte`. A missing Go map key or an
unmapped struct field remains absent; conversion does not invent entries for
optional schema fields.

Pointers and interfaces are unwrapped. Cycles in pointers, maps, or slices are
rejected with an input error. Repeated references are allowed when they are not
an ancestor cycle: each occurrence is converted into independent immutable
HFF storage. The source must not be mutated while `Convert` runs.

## Limits, ownership, and errors

`Converter` snapshots its schema, limits, and struct mappings at construction.
Later changes to the `Config`, mappings, or option inputs do not alter it. The
returned HFF values own their collection storage and are immutable. A converter
can be used concurrently, provided each source record remains unchanged during
conversion.

Conversion and schema validation are context-aware and apply the input depth
and value limits. During Go traversal, the root starts at depth zero; each
non-nil pointer or interface wrapper and each child value adds one depth and
one visited value. Nil wrappers become null without visiting a child. The
resulting HFF value is then checked against the schema under the same input
limits. Other fields in `hff.Limits`, such as runtime step and generated-value
budgets, do not govern this adapter's conversion. Zero input limits select the
documented finite defaults. Use `WithLimits` to override selected limits or
`WithUnlimitedLimits` to opt out explicitly. Options are applied from left to
right; a later limit option overrides an earlier one.

Configuration problems, unsupported Go representations, cycles, invalid
values, schema mismatches, limit exhaustion, and cancellation return typed
errors. Use `errors.As` to inspect `*hff.Error` and its code/path rather than
matching error text. Errors return no partially converted value. This adapter
converts inputs only; HFF does not mutate Go records or write values back.

## Run the example

From the repository root, run:

```sh
go run ./examples/go-values
```

The example prints `Match: true` after converting a Go struct and evaluating
the resulting HFF value with a local filter.

## Infer a schema and bind a Go record type

When the Go type itself is the service contract, `NewBinding[T]` derives an
HFF schema and immutable conversion plan together. The resulting typed filter
accepts `T` directly:

```go
type Person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

binding, err := govalue.NewBinding[Person]()
if err != nil {
	return err
}
e, err := engine.New(
	engine.WithSchema(binding.Schema()),
	engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
	return err
}
program, err := e.Compile(ctx, `age>=18 name`)
if err != nil {
	return err
}
filter, err := binding.Build(ctx, program)
if err != nil {
	return err
}
matched, err := filter.Match(ctx, Person{Name: "Alice", Age: 24})
```

`Build` requires the program's schema to equal the inferred schema. The
`Filter[T]` retains that binding and converts each input record before local
matching. `Binding.Convert` is available when a caller needs an explicit
`hff.Value`; unlike `Converter`, an inferred binding does not accept a second
schema or custom struct mapping.

Exported struct fields become HFF fields. A field's name comes from its `hff`
tag, then its `json` tag, then its Go field name. A `-` tag omits the field;
`omitempty` marks it optional and uses Go's `encoding/json` empty-value rule.
Other tag options are rejected. Embedded structs are promoted using the same
name rules. If promotion or tags yield duplicate HFF names, inference fails
rather than choosing one field. Unsupported or recursive Go types also fail
when the binding is created.

For typed local filters, the inferred schema must have an object root: use a
struct or string-keyed map. A pointer to either object type is also accepted as
`T`, but the root record must be non-nil; converting a nil root returns an
`InputError`. `NewBinding[T]` also supports non-object types for conversion-only
use through `Binding.Convert`; `Build` cannot create a filter for those schemas.
Nested nil pointers, maps, slices, and interfaces convert to HFF null. Non-nil
empty maps and slices remain empty objects and lists. Numeric kinds are
preserved without coercion, and `time.Time`, `time.Duration`, and `hff.Value`
have their corresponding HFF representations. The binding snapshots the
inferred schema and conversion plan. Do not mutate a source value while it is
being converted or matched. Runtime cycles in pointers, maps, or slices are
rejected; repeated non-cyclic references are converted independently.

The Go traversal and the resulting HFF validation each apply the configured
input depth/value limits. Pointer and interface wrappers count toward Go
traversal depth and visited values. Other runtime budgets do not govern this
conversion adapter. The typed-binding path is shown end to end by the
[runnable example](https://github.com/ornew/hff/tree/main/examples/go-binding):

```sh
go run ./examples/go-binding
```
