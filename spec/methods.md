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
In query source, parentheses may be omitted when a method call supplies no
arguments, including when all declared arguments are optional. Required
arguments still produce a compile-time error when omitted. Method names in
prose and signatures in this catalog retain parentheses; sample query syntax
may omit parentheses when no argument is supplied. `func(T) -> U` is
explanatory callback notation, not HFF source syntax.
Counts, timestamp components, and
`duration:nanoseconds()` return int64. Other duration unit conversions return
float64; `round()`, `ceil()`, and `floor()` preserve the input numeric subtype.

## Any value

| Signature | Purpose |
|:--|:--|
| `any:type() -> string` | Runtime type name: `bool`, `number`, `string`, `list`, `object`, `bytes`, `timestamp`, `duration`, or `null` |
| `any:json() -> string` | JSON serialization |
| `any:string() -> string` | String conversion |
| `any:in(any...) -> bool` | Membership among the arguments |
| `any:exists() -> bool` | True for any present, non-null value |

On a null receiver, `type()`, `json()`, and zero-argument `string()` return
`"null"`. The timestamp-specific `string(tz)` overload still requires a zone.
The zero-argument `any:string()` formats timestamps in UTC; the required-zone
`timestamp:string(tz)` overload is a separate method and capability.
`exists()` returns false for null or guarded no-value, and true for every other
value, including false, zero, and empty collections. It deliberately observes
presence rather than truthiness. On guarded no-value, `type()`, `json()`, and
zero-argument `string()` propagate no-value unchanged. Other methods propagate
null and guarded no-value, subject to predicate callback conversion described
below.
`in()` requires at least one argument and tests membership among its arguments.

`string()` leaves strings unquoted, formats booleans and numbers in ordinary
lowercase/decimal notation, emits compact JSON for lists and objects, standard
base64 for bytes, UTC RFC 3339 for timestamps, and compound h/m/s for durations
with fractional seconds representing nanoseconds. `json()` produces compact
JSON and represents bytes, timestamps, and durations as these strings.
Object keys are sorted in code-point order. Non-JSON type information is not
restored automatically by `fromjson()`. Float64 values retain a decimal point
or exponent marker to preserve the numeric subtype. Their exact decimal
spelling and signed-zero text follow the selected backend's `float.format`
Profile detail. Integer values use integer decimal spelling. Zero duration
is written `0s`.

## Booleans and numbers

| Signature | Purpose |
|:--|:--|
| `bool:and(bool) -> bool` | Logical AND |
| `bool:or(bool) -> bool` | Logical OR |
| `bool:not() -> bool` | Logical NOT |
| `number:round() -> number` | Round to an integer value |
| `number:ceil() -> number` | Round upward |
| `number:floor() -> number` | Round downward |

For Int, `round()`, `ceil()`, and `floor()` are identity operations. Float
`round()`, `ceil()`, and `floor()` follow the selected backend's
`float.rounding` Profile detail. Numbers distinguish int64 and float64;
rounding methods preserve the numeric subtype.

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
| `string:frombase64r() -> bytes` | Decode unpadded standard base64 |
| `bytes:len() -> number` | Byte count |
| `bytes:base64() -> string` | Encode base64 |
| `bytes:base64r() -> string` | Encode unpadded standard base64 |

String length counts Unicode code points. Case conversion uses locale-independent
Unicode simple mappings, without multi-code-point expansions;
strings are not implicitly normalized. `contains()` requires at least one
argument and tests every supplied substring. `matches()` uses RE2 substring
matching; anchors select full-string matching. Invalid patterns and invalid
conversions yield null. HFF's regex reference syntax follows RE2 except the
byte escape `\C`, which is excluded and therefore invalid. A backend may
support a smaller regex subset only when its support matrix or compatibility
profile documents the restriction. `base64()` and `frombase64()` use the
standard alphabet with required padding. `base64r()` and `frombase64r()` use
the same standard alphabet without padding; an empty string represents empty
bytes. The encoders emit canonical output for their respective forms. The
decoders reject URL-safe characters, embedded CR/LF, and non-canonical pad
bits. Invalid base64 returns null, and null propagates through the conversion
methods.

`bool()` accepts exactly `true` or `false`. `number()` accepts JSON numeric
spelling and follows literal subtype selection. Neither trims surrounding
whitespace. `fromjson()` rejects duplicate keys and out-of-range numbers with
a null result, even when its receiver is a constant string. JSON parsing and
canonical JSON serialization use the dedicated `MaxJSONDepth` resource limit:
64 by default, counting the JSON root at depth one and incrementing for each
nested array or object. It applies to `fromjson()`, `json()`, and list/object
`string()` serialization. A scalar `string()` value has depth one. Exceeding
the limit aborts evaluation with a limit error rather than returning null or a
partial serialization.

## Lists

| Signature | Purpose |
|:--|:--|
| `list:size() -> number` | Element count |
| `list:count() -> number` | Element count |
| `list:count(func(T) -> bool) -> number` | Count elements whose callback result converts to true |
| `list:filter(func(T) -> bool) -> list` | Keep elements whose callback result converts to true |
| `list:sum() -> number` | Sum numeric elements |
| `list:sum(func(T) -> number) -> number` | Sum numeric callback results |
| `list:min() -> number` | Smallest numeric element |
| `list:min(func(T) -> number) -> number` | Smallest numeric callback result |
| `list:max() -> number` | Largest numeric element |
| `list:max(func(T) -> number) -> number` | Largest numeric callback result |
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
`count()` without a callback returns the element count. With a predicate
callback, it counts elements whose callback result converts to true. The
callback is evaluated for each element; guarded no-value converts to false for
that element. An empty list counts as zero. A null receiver yields null.
`filter(predicate)` retains the original elements whose callback result
converts to true, in their original order; it does not transform them. Guarded
no-value converts to false for that element. Filtering an empty list returns
an empty list, and a null receiver yields null.
`sum()` accepts a list of numeric values or one callback that projects each
element to a numeric value. Null values and guarded no-value callback results
are skipped. A nonnumeric runtime value makes the result null. An empty list or
one with no contributing values sums to Int zero. If every contribution is Int,
the exact total is returned when it fits int64; otherwise the result is null. If
any contribution is Float, the result is Float and its arithmetic follows the
backend's declared Float model; a non-finite result is null. A null receiver
yields null. The zero-argument method requires
`method:list.sum`; the callback form also requires `Callbacks`. Cancellation or
a resource-limit error aborts instead of returning a partial sum.
`min()` and `max()` select from numeric list elements or numeric callback
results. Null values and guarded no-value callback results are skipped. A
nonnumeric runtime value makes the result null; an empty list or one with no
contributing values also returns null. Mixed Int and Float values are compared
exactly without rounding either value first. The selected original value is
returned with its original numeric subtype (the numeric callback result when a
projection callback is used), and the first value wins a tie. A null receiver
yields null. The zero-argument forms require `method:list.min`
and `method:list.max` respectively; callback forms also require `Callbacks`.
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

`count(predicate)` and `filter(predicate)` use the same callback syntax and
ordinary boolean conversion, but inspect the whole list because their result
depends on every element. Callback order is left to right. Cancellation or a
resource-limit error aborts the method rather than returning a partial count or
list.

```hff
items:any(x=>x.price>10)
items:all(x=>x.active)
```

Callbacks use `x=>expr` or `(acc,x)=>expr`. Parameter types are inferred, and
callbacks may reference outer schema variables. There are no general function
declarations or recursion. `map()`, `flatmap()`, and `foldl()` visit elements
left-to-right; `foldr()` visits right-to-left. The accumulator type `A` may differ
from the element type `T`. An empty fold returns the initial accumulator.

Callbacks may nest. Inner callbacks can capture unshadowed outer callback
parameters; a nested parameter may shadow an outer name. Nested callback
expressions require the `NestedCallbacks` capability, which depends on
`Callbacks`. `MaxCallbackDepth` defaults to 64; the outermost callback has depth
1. AND and OR use the ordinary left-to-right short-circuit semantics in callback
bodies. Compilation also bounds semantic work with `MaxCompileSteps`; local
execution separately charges evaluated callback work to its per-match
`MaxSteps` budget.

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

The component methods accept an optional time-zone argument and default to UTC.
`timestamp:string(tz)` requires an explicit time zone. Timestamps have nanosecond
precision and format as RFC 3339, omitting trailing fractional zeros. Invalid or
unrepresentable timestamps yield null. Years range from 0000 through 9999. Month, day-of-month,
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

The local backend accepts named-zone data through `local.TimeZoneProfile`,
which contains a nonempty version label, a map of zone names to TZif bytes, and
optional aliases. `local.WithTimeZones` or `local.Config.TimeZones` supplies the
profile to `local.Build`. Build copies and parses the data into the filter; a
later Match performs no filesystem or environment lookup. Aliases point
directly to a zone in the profile or to UTC; alias chains are not accepted. UTC
and fixed offsets are built in and cannot be overridden by a profile. Invalid
profiles are configuration errors. `Filter.TimeZoneVersion()` returns the
profile's version label, or the empty string when only UTC and fixed offsets
are available.
