# Method Reference

This page is a receiver-oriented lookup of built-in method signatures and
their direct results. For edge cases and normative evaluation rules, follow
the links to the [language method specification](../../spec/methods.md).
Backend availability is listed in [Backends](../backends/README.md). Query
examples may omit parentheses on calls with no arguments; signatures here keep
their parentheses. `func(T) -> U` is explanatory callback notation.

## Any

| Signature | Result and use |
|:--|:--|
| `any:type() -> string` | Runtime type name. |
| `any:json() -> string` | Compact canonical JSON representation. |
| `any:string() -> string` | Human-readable conversion; structured values use compact JSON and timestamps use UTC. |
| `any:in(any...) -> bool` | True when the receiver equals any argument; requires one or more. |
| `any:exists() -> bool` | True for present, non-null values, including false, zero, and empty collections. |

See [Any value](../../spec/methods.md#any-value) for null behavior and
serialization details.

## Boolean and number

| Signature | Result and use |
|:--|:--|
| `bool:and(bool) -> bool` | Logical AND with left-to-right short-circuit evaluation. |
| `bool:or(bool) -> bool` | Logical OR with left-to-right short-circuit evaluation. |
| `bool:not() -> bool` | Logical negation. |
| `number:round() -> number` | Int identity; Float rounding follows the backend Profile. |
| `number:ceil() -> number` | Int identity; Float rounding follows the backend Profile. |
| `number:floor() -> number` | Int identity; Float rounding follows the backend Profile. |

Numeric rounding preserves int64 versus float64 subtype. Int rounding methods
are identity operations; Float `round()`, `ceil()`, and `floor()` follow the
backend's `float.rounding` Profile detail. See
[boolean and number rules](../../spec/methods.md#booleans-and-numbers).

## String and bytes

| Signature | Result and use |
|:--|:--|
| `string:len() -> number` | Unicode code-point count. |
| `string:contains(string...) -> bool` | All supplied substrings occur; requires at least one argument. |
| `string:matches(string) -> bool` | RE2 substring match; use anchors for a full-string match. |
| `string:startswith(string) -> bool` | Prefix test. |
| `string:endswith(string) -> bool` | Suffix test. |
| `string:lower() -> string` | Locale-independent Unicode simple lowercase mapping. |
| `string:upper() -> string` | Locale-independent Unicode simple uppercase mapping. |
| `string:timestamp() -> timestamp` | Parse RFC 3339 timestamp text. |
| `string:duration() -> duration` | Parse a fixed duration string. |
| `string:bool() -> bool` | Parse exactly `true` or `false`. |
| `string:number() -> number` | Parse JSON numeric spelling. |
| `string:fromjson() -> any` | Parse strict JSON into HFF values. |
| `string:concat(string) -> string` | Append the argument to the receiver. |
| `string:frombase64() -> bytes` | Decode standard padded base64. |
| `string:frombase64r() -> bytes` | Decode unpadded standard base64. |
| `bytes:len() -> number` | Byte count. |
| `bytes:base64() -> string` | Encode standard padded base64. |
| `bytes:base64r() -> string` | Encode unpadded standard base64. |

The PostgreSQL backend supports scalar Bytes values backed by `bytea`, including
`len()`, both base64 encoders, equality, and decoding with either string
decoder. It also
supports schema-known Bytes values in JSON/JSONB collections, represented as
canonical padded base64 strings. An `Any` string is not inferred to be Bytes,
and an ambiguous `String|Bytes` union cannot recover the distinction. Typed
Bytes membership with `in()` is supported; dynamic `Any`-versus-Bytes and
generic dynamic tree comparisons involving Bytes remain unsupported. See the
[PostgreSQL bytea guide](../backends/postgresql/README.md#bytes-and-base64) and
[JSON Bytes guide](../backends/postgresql/json.md#bytes-in-json-documents).
PostgreSQL 15 and later also supports `string:matches()` for literal patterns
in the documented [RE2 subset](../backends/postgresql/README.md#regular-expressions).
PostgreSQL supports `list:sort()` for JSON-backed lists, including recoverable
`Any` and list-union values; JSONB numeric subtype restoration may require
type metadata. See the
[JSON guide](../backends/postgresql/json.md#sort-json-backed-lists) for the
supported profile.

Invalid conversions and invalid regular expressions yield null. The HFF regex
reference is RE2 except `\\C`, which is rejected. Padded base64 methods require
canonical padding; the `r` methods require canonical unpadded standard base64.
Both decoders reject URL-safe characters and embedded line breaks, and reject
non-canonical pad bits or padding for their respective forms.
See [string and byte rules](../../spec/methods.md#strings-and-bytes).

## List

| Signature | Result and use |
|:--|:--|
| `list:size() -> number` | Element count. |
| `list:count() -> number` | Element count. |
| `list:count(func(T) -> bool) -> number` | Count elements whose callback result converts to true. |
| `list:filter(func(T) -> bool) -> list` | Keep original elements whose callback result converts to true. |
| `list:sum() -> number` | Sum numeric elements. |
| `list:sum(func(T) -> number) -> number` | Sum numeric callback results. |
| `list:avg() -> float` | Average numeric elements. |
| `list:avg(func(T) -> number) -> float` | Average numeric callback results. |
| `list:min() -> number` | Select the smallest numeric element. |
| `list:min(func(T) -> number) -> number` | Smallest numeric callback result. |
| `list:max() -> number` | Select the largest numeric element. |
| `list:max(func(T) -> number) -> number` | Largest numeric callback result. |
| `list:distinct() -> list<T>` | Stable deduplicated copy using HFF equality. |
| `list:find(func(T) -> bool) -> T|null` | First original element matching the callback. |
| `list:has(number) -> bool` | Whether an integer-valued index exists; negative indexes count from the end. |
| `list:contains(any...) -> bool` | Whether every supplied value is an element; requires at least one argument. |
| `list:unique() -> bool` | Whether all elements are distinct by HFF equality. |
| `list:sort() -> list` | Stable sorted copy; invalid runtime sort domains yield null. |
| `list:concat(list) -> list` | New list with the argument appended. |
| `list:any(func(T) -> bool) -> bool` | Whether any element's callback result converts to true. |
| `list:all(func(T) -> bool) -> bool` | Whether every element's callback result converts to true. |
| `list:map(func(T) -> U) -> list` | Transform each element; null callback results remain list elements. |
| `list:flatmap(func(T) -> list) -> list` | Transform and concatenate returned lists. |
| `list:foldl(A, func(A, T) -> A) -> A` | Left fold with callback arguments `(acc, item)`. |
| `list:foldr(A, func(T, A) -> A) -> A` | Right fold with callback arguments `(item, acc)`. |

`any()` and `all()` short-circuit in list order; empty-list results are false
and true respectively. Callback types are inferred. Nested callbacks require
the `NestedCallbacks` capability and are bounded by `MaxCallbackDepth`.
`count()` without a callback returns the element count. Its predicate form
counts truthy callback results, treating guarded no-value as false for that
element. `filter(predicate)` keeps the matching original values in order;
guarded no-value is false, and filtering an empty list returns an empty list.
Both methods return null for a null receiver. Callback forms require the
`Callbacks` capability. Backend support is conditional on the bound
representation and, for an ordered relation result from `filter()`, a unique
order mapping.
`sum()` skips null values; its callback form also skips guarded no-value.
An empty list or one with no contributing values returns Int zero. A runtime
nonnumeric contribution returns null. All-Int contributions are accumulated
exactly and return null if the final total does not fit int64. If any
contribution is Float, the result is Float and follows the backend's Float
Profile; non-finite results return null. A null receiver yields null. The
zero-argument form requires `method:list.sum`; callback forms also require the
`Callbacks` capability.
`avg()` accepts numeric elements or numeric callback projections. It skips null
and guarded no-value results. A nonnumeric runtime value, empty list, or
all-skipped list returns null. The result is always Float. In Local, Int values
accumulate exactly until the first Float contribution; from then on, Go
`float64` addition follows list order. If all contributions are Int, the exact
total converts to Float before division by the contribution count. A non-finite
result returns null, and a null receiver yields null. The zero-argument form
requires `method:list.avg`; the callback form also requires `Callbacks`.
PostgreSQL JSON/JSONB list bindings support `avg()` when numeric subtypes are
recoverable, including callback projections. Float inputs pass through
round-trip-safe Float text before `AVG(numeric)` and a checked final Float
conversion. This requires `extra_float_digits > 0`; HFF does not set the
database session value. Intermediate overflow, cancellation, rounding, and
zero sign can differ from Local's list-order Float accumulation. PostgreSQL
relation `avg()` also supports numeric callback projections when the bound
values preserve their numeric subtypes. It does not require a unique relation
order unless a nested operation observes order. Native scalar-column `avg()`
is not currently supported.
`min()` and `max()` accept numeric elements or numeric callback projections.
They skip null values and guarded no-value results. A nonnumeric runtime value,
an empty list, or a list with no contributing values returns null. Mixed Int and
Float values are compared exactly; the selected original value and subtype are
returned (the callback result in projected forms), with the first value winning
ties. A null receiver yields null. Their zero-argument forms require
`method:list.min` and `method:list.max` respectively; callback forms also
require `Callbacks`. Local supports both methods. PostgreSQL supports JSON/JSONB
lists when numeric subtypes are recoverable, including `Any` values whose
numeric subtype can be recovered at runtime. JSONB requires version-1 numeric
type metadata when storage loses the Int/Float distinction. PostgreSQL relation
bindings support numeric callback projections: Int-only or null-only results
do not require unique relation order, while Float or mixed results do.
PostgreSQL `Build` can reject a JSON or relation binding when the required
representation or ordering is unavailable.
See [list rules](../../spec/methods.md#lists) and the
[callback guide](../guides/callbacks.md).

`distinct()` keeps the first value from each HFF-equal group, including one null
and the first representation of equal numeric values. It requires
`method:list.distinct` and the structural equality capabilities applicable to
the element type. `find(predicate)` returns the first original matching value,
not the callback result; it requires `method:list.find` and `Callbacks`. Guarded
no-value predicates count as false, and a null receiver yields null. Local and
schema-known PostgreSQL JSON/JSONB lists support both when the bound
representation is recoverable. PostgreSQL relations require `WithUniqueOrder`
because both results observe first-occurrence order.

For PostgreSQL, schema-known JSON/JSONB lists support no-argument and callback
`sum()` for recoverable Int, Float, and mixed Int/Float contributions,
including `Any` lists and callback results when numeric subtypes can be
recovered. JSONB needs version-1 numeric type metadata when stored values may
have lost that distinction. Float sums use the backend's native Float model.
For PostgreSQL JSON/JSONB Float sums, each input is converted using
round-trip-safe Float text and accumulated with `SUM(numeric)` before a checked
Float conversion. This requires `extra_float_digits > 0`; HFF does not set the
database session value. The numeric intermediate can differ from Local's
list-order Float addition.
Schema-known JSON/JSONB lists support `count()` and `filter(predicate)` when
the callback and retained values can be represented.
Bound relations support both count forms and statically Int sum projections
without a unique order; Float and dynamic relation sums remain unsupported.
Relation `filter(predicate)` requires a unique order because it returns a list.
Schema-known JSON/JSONB lists also support `contains()`, `unique()`, `sort()`,
`concat(list)`, `any()`, and `all()`, plus `map()` and `flatmap()` when callback
result types are known; scalar `in()` is also supported inside callbacks.
Folds are supported with known accumulator types. `json()` and `string()`
support null, bool, int64, string, and finite float values from scalar columns
and native JSON in the schema-known profile.
JSONB Float serialization requires metadata when storage may have lost an
observable representation such as signed zero. Float text follows the
backend's `float.format` Profile detail. PostgreSQL supports `fromjson()` for
the representations listed in its JSON support matrix. Structural comparisons
require the corresponding equality capability. See the
[PostgreSQL JSON support matrix](../backends/postgresql/json.md) for the exact
supported profile and restrictions.

## Object

| Signature | Result and use |
|:--|:--|
| `object:size() -> number` | Number of entries. |
| `object:has(string) -> bool` | Whether a key exists, including a key whose value is null. |
| `object:get(string) -> any` | Value for a key; a missing key returns null. |
| `object:keys() -> list` | Keys in Unicode code-point order. |
| `object:values() -> list` | Values in the same order as `keys()`. |
| `object:items() -> list` | `[key, value]` pairs in key order. |

See [object rules](../../spec/methods.md#objects) to distinguish missing keys
from stored nulls.

## Timestamp

| Signature | Result and use |
|:--|:--|
| `timestamp:year([tz: string]) -> number` | Calendar year; UTC by default. |
| `timestamp:month([tz: string]) -> number` | One-based month; UTC by default. |
| `timestamp:dayofmonth([tz: string]) -> number` | One-based day; UTC by default. |
| `timestamp:dayofyear([tz: string]) -> number` | One-based day; UTC by default. |
| `timestamp:hours([tz: string]) -> number` | Hour component; UTC by default. |
| `timestamp:minutes([tz: string]) -> number` | Minute component; UTC by default. |
| `timestamp:seconds([tz: string]) -> number` | Second component; UTC by default. |
| `timestamp:milliseconds([tz: string]) -> number` | Millisecond component; UTC by default. |
| `timestamp:string(tz: string) -> string` | RFC 3339 rendering in the required zone; unlike `any:string()`, this method requires a zone. |
| `timestamp:after(timestamp) -> bool` | Later-than comparison. |
| `timestamp:before(timestamp) -> bool` | Earlier-than comparison. |
| `timestamp:add(duration) -> timestamp` | Add fixed elapsed time. |

Years range from 0000 through 9999; timestamp precision is nanoseconds.
Formatting does not silently change instants that cannot be represented in
RFC 3339. See [timestamp rules](../../spec/methods.md#timestamps) and the
[time zone profile guide](../guides/temporal.md#supply-versioned-time-zone-data).

## Duration

| Signature | Result and use |
|:--|:--|
| `duration:hours() -> number` | Total hours as float64. |
| `duration:minutes() -> number` | Total minutes as float64. |
| `duration:seconds() -> number` | Total seconds as float64. |
| `duration:milliseconds() -> number` | Total milliseconds as float64. |
| `duration:microseconds() -> number` | Total microseconds as float64. |
| `duration:nanoseconds() -> number` | Total nanoseconds as int64. |

Duration strings accept signed, compound decimal units `h`, `m`, `s`, `ms`,
`us`, and `ns`; values must sum to an exact integral nanosecond within int64
range. Days and weeks are not supported. See
[duration rules](../../spec/methods.md#durations).
The PostgreSQL backend can bind a schema-known Duration scalar to a `bigint`
of signed nanoseconds; JSON/JSONB and composite Duration storage are not
supported. See the [temporal guide](../guides/temporal.md#store-native-postgresql-durations).

The PostgreSQL backend can also decode schema-known Duration values stored as
canonical strings in JSON or JSONB with `BindJSON`. A JSON string under `Any`
remains a String, and ambiguous same-position `String|Duration` unions cannot
be recovered. Alongside schema-known Duration and nullable Duration shapes,
the binding supports a limited operation subset for the schema-known unions
`Duration|Int|Null`, `Duration|Bool|Null`, and `Duration|Int|Bool|Null`. Other
union shapes or operations may be rejected when the active type cannot be
recovered or the requested operation cannot be lowered. These JSON
representations are separate from the native scalar Duration binding and do
not add Duration tags to JSONB numeric metadata. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#duration-values-in-json-documents).

## Related references

- [Normative method semantics](../../spec/methods.md)
- [Capability IDs and selection](../../spec/capabilities.md)
- [Local backend support and configuration](../backends/local/README.md)
- [PostgreSQL backend support](../backends/postgresql/README.md)
- [Typed IR Reference](program-ir.md)
