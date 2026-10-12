# Converting Go Values

The optional `adapters/govalue` package converts ordinary Go records into
owned, schema-checked HFF values. The application still declares the HFF schema
explicitly; conversion does not infer a query contract from a Go type.

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
fmt.Println(matched)
```

The converter validates its result against the configured schema before it
returns. `Match` validates again as part of its normal input contract; when a
caller already has an `engine.Input`, use `MatchInput` to reuse that validated
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
