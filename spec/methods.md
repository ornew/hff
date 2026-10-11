# Methods

Methods are selected at compile time. A backend may support only some methods.
Parsing a method call does not imply that it can be compiled. Calls on a
statically incompatible receiver are compile-time errors. A runtime type that
matches no applicable, enabled signature yields null, but every statically
applicable signature on a union or `Any` must be enabled at compile time. A
value-only `null` receiver likewise has no type family to select a single
signature, so all statically applicable candidates must be enabled. To permit
only selected families, use the explicit `value:[Type]` guard described in
[syntax](syntax.md#explicit-type-guards) and [types](types.md#explicit-type-guards).
The `type()` method remains an ordinary string-valued runtime method and has no
special flow-narrowing behavior.

The catalog below records the supplied signatures. `any...` and `string...`
denote variadic arguments; brackets in a signature denote an optional argument.
`func(T) -> U` is explanatory callback notation, not HFF source syntax.
Counts, timestamp components, and
`duration:nanoseconds()` return int64. Other duration unit conversions return
float64; `round()`, `ceil()`, and `floor()` preserve the input numeric subtype.

## Any value

| Signature | Purpose |
|:--|:--|
| `any:type() -> string` | Runtime type name: `bool`, `number`, `string`, `list`, `object`, `bytes`, `timestamp`, `duration`, or `null` |
| `any:tojson() -> string` | JSON serialization |
| `any:tostring() -> string` | String conversion |
| `any:in(any...) -> bool` | Membership among the arguments |
| `any:exists() -> bool` | True for any present, non-null value |

On a null receiver, `type()`, `tojson()`, and `tostring()` return `"null"`.
`exists()` returns false for null or guarded no-value, and true for every other
value, including false, zero, and empty collections. It deliberately observes
presence rather than truthiness. Other methods propagate null and guarded
no-value, subject to predicate callback conversion described below.
`in()` requires at least one argument and tests membership among its arguments.

`tostring()` leaves strings unquoted, formats booleans and numbers in ordinary
lowercase/decimal notation, emits compact JSON for lists and objects, standard
base64 for bytes, UTC RFC 3339 for timestamps, and compound h/m/s for durations
with fractional seconds representing nanoseconds. `tojson()` produces compact
JSON and represents bytes, timestamps, and durations as these strings.
Object keys are sorted in code-point order. Non-JSON type information is not
restored automatically by `fromjson()`. Float64 values use a shortest
round-trip representation, retaining a decimal point or exponent to preserve the
numeric subtype. Negative float64 zero is written `-0.0`. Integer values use
integer decimal spelling. Zero duration is written `0s`.

## Booleans and numbers

| Signature | Purpose |
|:--|:--|
| `bool:and(bool) -> bool` | Logical AND |
| `bool:or(bool) -> bool` | Logical OR |
| `bool:not() -> bool` | Logical NOT |
| `number:round() -> number` | Round to an integer value |
| `number:ceil() -> number` | Round upward |
| `number:floor() -> number` | Round downward |

`round()` rounds halfway values away from zero: `2.5` becomes `3`, and `-2.5`
becomes `-3`. Numbers distinguish int64 and float64; rounding methods preserve
the numeric subtype.

## Strings and bytes

| Signature | Purpose |
|:--|:--|
| `string:len() -> number` | String length |
| `string:contains(string...) -> bool` | All supplied substrings are present; at least one argument |
| `string:matches(string) -> bool` | RE2 matching |
| `string:startswith(string) -> bool` | Prefix test |
| `string:endswith(string) -> bool` | Suffix test |
| `string:lower() -> string` | Lowercase conversion |
| `string:upper() -> string` | Uppercase conversion |
| `string:timestamp() -> timestamp` | Parse an RFC 3339 string |
| `string:duration() -> duration` | Parse a duration string |
| `string:bool() -> bool` | Parse a boolean |
| `string:number() -> number` | Parse a number |
| `string:fromjson() -> any` | Parse JSON |
| `string:concat(string) -> string` | Concatenation |
| `string:frombase64() -> bytes` | Decode base64 |
| `bytes:len() -> number` | Byte count |
| `bytes:tobase64() -> string` | Encode base64 |

String length counts Unicode code points. Case conversion uses locale-independent
Unicode simple mappings, without multi-code-point expansions;
strings are not implicitly normalized. `contains()` requires at least one
argument and tests every supplied substring. `matches()` uses RE2 substring
matching; anchors select full-string matching. Invalid patterns and invalid
conversions yield null. HFF's regex reference syntax follows RE2 except the
byte escape `\C`, which is excluded and therefore invalid. A backend may
support a smaller regex subset only when its support matrix or compatibility
profile documents the restriction. Base64 uses the standard alphabet and
required padding; URL-safe and unpadded alternatives are not accepted.

`bool()` accepts exactly `true` or `false`. `number()` accepts JSON numeric
spelling and follows literal subtype selection. Neither trims surrounding
whitespace. `fromjson()` rejects duplicate keys and out-of-range numbers with
a null result, even when its receiver is a constant string.

## Lists

| Signature | Purpose |
|:--|:--|
| `list:size() -> number` | Element count |
| `list:has(number) -> bool` | Index-presence test |
| `list:contains(any...) -> bool` | All supplied elements are present; at least one argument |
| `list:unique() -> bool` | Whether every element is distinct |
| `list:sort() -> list` | Sorted list |
| `list:concat(list) -> list` | Concatenation |
| `list:any(func(T) -> bool) -> bool` | At least one matching element |
| `list:all(func(T) -> bool) -> bool` | Every element matches |
| `list:map(func(T) -> U) -> list` | Element transformation |
| `list:flatmap(func(T) -> list) -> list` | Transformation followed by flattening |
| `list:foldl(A, func(A, T) -> A) -> A` | Left fold, callback arguments `(acc, x)` |
| `list:foldr(A, func(T, A) -> A) -> A` | Right fold, callback arguments `(x, acc)` |

List element count uses `size()`, including implicit boolean conversion.
`has()` supports the same negative indices as element access. `unique()` returns
whether every element is distinct using structural equality. `has()` returns
false for an out-of-range integer-valued index, and null for a non-integer index. `sort()` is stable
and requires a shared orderable type; mixed numeric subtypes are allowed.
An invalid runtime sort domain yields null. Methods never mutate the receiver.

`any()` and `all()` evaluate predicate callbacks left-to-right with ordinary
boolean conversion of callback results; the displayed bool result signature
includes that contextual conversion. They short-circuit when their answer is
known. Empty lists return false for any and true for all. Null receivers yield
null. Guarded no-value from a predicate callback converts to false for that
element and does not invalidate other elements. A cancellation still aborts the
call; it is never a false predicate result.

```hff
items:any(x=>x.price>10)
items:all(x=>x.active)
```

Callbacks use `x=>expr` or `(acc,x)=>expr`. Parameter types are inferred, and
callbacks may reference outer schema variables. There are no general function
declarations or recursion. `map()`, `flatmap()`, and `foldl()` visit elements
left-to-right; `foldr()` visits right-to-left. The accumulator type `A` may differ
from the element type `T`. An empty fold returns the initial accumulator.

`map()` preserves null callback results as list elements. A non-list result in
`flatmap()` yields null for the entire method. Guarded no-value in a callback
propagates to the entire method; failed elements are never silently skipped.

## Objects

| Signature | Purpose |
|:--|:--|
| `object:size() -> number` | Field or entry count |
| `object:has(string) -> bool` | Field or key presence |
| `object:get(string) -> any` | Field or key lookup |
| `object:keys() -> list` | Keys |
| `object:values() -> list` | Values |
| `object:items() -> list` | Key/value entries |

`has()` tests presence, including keys whose value is null. `keys()`, `values()`, and `items()`
enumerate in code-point key order. Each item is a two-element list `[key, value]`;
values and items follow the same ordering as keys.
`get()` returns null for a missing key.

## Timestamps

| Signature | Purpose |
|:--|:--|
| `timestamp:year([tz: string]) -> number` | Calendar year |
| `timestamp:month([tz: string]) -> number` | Calendar month |
| `timestamp:dayofmonth([tz: string]) -> number` | Day of month |
| `timestamp:dayofyear([tz: string]) -> number` | Day of year |
| `timestamp:hours([tz: string]) -> number` | Hour component |
| `timestamp:minutes([tz: string]) -> number` | Minute component |
| `timestamp:seconds([tz: string]) -> number` | Second component |
| `timestamp:milliseconds([tz: string]) -> number` | Millisecond component |
| `timestamp:string(tz: string) -> string` | String rendering in a time zone |
| `timestamp:after(timestamp) -> bool` | Later-than test |
| `timestamp:before(timestamp) -> bool` | Earlier-than test |
| `timestamp:add(duration) -> timestamp` | Time plus duration |

Omitted time zones default to UTC. Timestamps have nanosecond precision and
format as RFC 3339, omitting trailing fractional zeros. Invalid or unrepresentable
timestamps yield null. Years range from 0000 through 9999. Month, day-of-month,
and day-of-year use one-based calendar numbering. Hours range from 0 through 23,
minutes and seconds from 0 through 59, and the millisecond component from 0
through 999. Leap seconds are not supported, and input fractional seconds have
at most nine digits. Methods never depend on the machine's local time zone.

## Durations

| Signature | Purpose |
|:--|:--|
| `duration:hours() -> number` | Duration in hours |
| `duration:minutes() -> number` | Duration in minutes |
| `duration:seconds() -> number` | Duration in seconds |
| `duration:milliseconds() -> number` | Duration in milliseconds |
| `duration:microseconds() -> number` | Duration in microseconds |
| `duration:nanoseconds() -> number` | Duration in nanoseconds |

Duration strings support these suffixes:

| Suffix | Unit |
|:--|:--|
| `h` | Hour |
| `m` | Minute |
| `s` | Second |
| `ms` | Millisecond |
| `us` | Microsecond |
| `ns` | Nanosecond |

The zero string `"0"`, negative values such as `"-1h"`, fractional values such
as `"-23.4s"`, and compound values such as `"1h34us"` are supported.

Units larger than an hour, including days and weeks, are not supported. This
avoids ambiguity involving calendar boundaries, time zones, locales, and leap
seconds. Fixed-duration arithmetic must not implicitly become calendar arithmetic.
Durations are signed int64 nanoseconds. Unit methods return total amounts,
not calendar components; overflow yields null. A leading plus or minus sign and
compound fractional units are allowed, including `+1h30m` and `.5h`. Only the
listed suffixes are supported. Fractional components are added exactly before checking the total against
nanosecond precision and int64 bounds. Values not exactly representable as nanoseconds
yield null rather than being truncated.

## Time zones

Accepted time zone forms are UTC, a named IANA or Joda-Time time zone, or a fixed
offset of the form `+HH:MM` or `-HH:MM`:

```text
timezone = "UTC" / longtz / fixedtz
longtz   = <an IANA or Joda-Time time zone name>
fixedtz  = ("+" / "-") digit digit ":" digit digit
digit    = [0-9]
```

Examples include `"Asia/Tokyo"`, `"America/New_York"`, and `"+09:00"`.
Fixed offsets range through `+23:59` and `-23:59`. Engines identify their time
zone data versions and explicitly supported Joda-Time aliases. Unknown or
unavailable names yield null. A backend must document supported zone data and
reject features whose HFF semantics it cannot preserve.
