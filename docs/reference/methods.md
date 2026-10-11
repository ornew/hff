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
| `any:tojson() -> string` | Compact canonical JSON representation. |
| `any:tostring() -> string` | Human-readable conversion; structured values use compact JSON. |
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
| `number:round() -> number` | Nearest integer value; halfway cases round away from zero. |
| `number:ceil() -> number` | Smallest integer value not less than the receiver. |
| `number:floor() -> number` | Greatest integer value not greater than the receiver. |

Numeric rounding preserves int64 versus float64 subtype. See
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
| `bytes:len() -> number` | Byte count. |
| `bytes:tobase64() -> string` | Encode standard padded base64. |

Invalid conversions and invalid regular expressions yield null. The HFF regex
reference is RE2 except `\\C`, which is rejected. Base64 decoding rejects
URL-safe or unpadded forms, embedded line breaks, and non-canonical pad bits.
See [string and byte rules](../../spec/methods.md#strings-and-bytes).

## List

| Signature | Result and use |
|:--|:--|
| `list:size() -> number` | Element count. |
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
See [list rules](../../spec/methods.md#lists) and the
[callback guide](../guides/callbacks.md).

The PostgreSQL backend currently supports schema-known JSON/JSONB list
`contains()`, `unique()`, `any()`, and `all()`, plus scalar `in()` inside those
callbacks. Structural comparisons require the corresponding equality
capability. See the [PostgreSQL JSON support matrix](../backends/postgresql/json.md)
for the exact supported profile and restrictions.

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
| `timestamp:string(tz: string) -> string` | RFC 3339 rendering in the required zone. |
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

## Related references

- [Normative method semantics](../../spec/methods.md)
- [Capability IDs and selection](../../spec/capabilities.md)
- [Local backend support and configuration](../backends/local/README.md)
- [PostgreSQL backend support](../backends/postgresql/README.md)
- [Compiled Program and Node accessors](program-ir.md)
