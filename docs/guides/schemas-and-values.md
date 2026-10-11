# Schemas and Immutable Values

## Fields and type descriptors

Named helpers create fields. Struct creates a type. Field is the general helper
for a name and a composed type:

```go
recordType := schema.Struct(
    schema.String("name"),
    schema.Field("score", schema.Union(schema.IntType, schema.FloatType)),
    schema.Map("labels", schema.StringType),
    schema.List("items", schema.Struct(
        schema.String("sku"),
        schema.Int("quantity"),
    )),
)
```

ListOf and MapOf create types for nested containers; List and Map create named
fields. Union flattens nested unions, sorts their descriptors, and deduplicates
identical members. Empty unions are Never; a list of Never accepts an empty list
but cannot contain a host value. Any accepts every valid HFF value.

Available descriptors are NullType, BoolType, IntType, FloatType, StringType,
BytesType, TimestampType, DurationType, AnyType, and NeverType. The corresponding
named field helpers include Bool, Int, Float, String, Bytes, Timestamp, Duration,
and Any. Use NullableType for a nullable type inside a list or map.

Struct keys are literal UTF-8 names, including names containing dots, empty
names, and names requiring quoting in HFF source. Duplicate declarations and
invalid UTF-8 names are configuration errors. Schema descriptor nesting is
currently limited to 64 levels. Returned field/member slices are defensive copies.

## Optional fields and null

```go
recordType := schema.Struct(
    schema.String("name"),
    schema.Optional(schema.String("nickname")),
    schema.Nullable(schema.Int("age")),
    schema.Optional(schema.Nullable(schema.String("email"))),
)
```

Optional permits absence without adding null to the field's value type. Nullable
adds null without marking the field optional. Under HFF's null-safe input rules,
a missing nullable field is accepted and reads as null, but is still absent
from the actual record. A plain Any field is required unless explicitly made Optional or Nullable;
its present value may still be null. Validation never creates a present null entry for a
missing key. Lookup returns a separate presence boolean.

```go
record := hff.Object(map[string]hff.Value{
    "name": hff.String("Alice"),
    "age": hff.Null(),
})
_, hasNickname := record.Lookup("nickname") // false
age, hasAge := record.Lookup("age")         // true
_ = age
_ = hasAge
_ = hasNickname
```

The zero Value is invalid. Construct explicit null with Null(). An invalid
value inside Any, a list, or an object remains an input error.

## Numeric and temporal values

Int accepts int64; Float accepts float64. Schema validation preserves this
choice and does not promote integers. Use a union when either subtype is valid.
NaN and infinities are invalid host input; subnormal floats and signed zero are
valid. Invalid UTF-8 strings and object keys are also rejected.

```go
value := hff.Timestamp(time.Date(2026, 10, 11, 0, 0, 0, 123, time.UTC))
interval := hff.Duration(30 * time.Second)
```

Timestamp removes Go's monotonic clock component and normalizes to UTC. Input
validation accepts years 0000 through 9999. Duration uses Go's signed int64
nanosecond representation. These constructors accept explicit values; they do
not parse strings or perform I/O.

## Ownership and accessors

Object, List, and Bytes copy supplied storage. AsObject, AsList, and AsBytes
return defensive copies. Nested Values are immutable, so copying handles is
safe. Changing the original map/slice or an accessor result cannot alter a
validated Input. Scalar accessors return `(value, bool)` and report false when
the value has another kind. Kind distinguishes numeric subtypes and null.

Constructors preserve invalid scalar data for path-aware input diagnostics.
Constructing a Value does not itself validate its contents. Pass it through an
engine's Validate before treating it as a valid record.
