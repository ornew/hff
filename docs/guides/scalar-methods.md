# Scalar and String Methods

This guide explains common scalar and string query patterns. The
[method reference](../reference/methods.md) indexes exact signatures and
contracts. Backend support is listed separately in the
[local backend guide](../backends/local/README.md); the local backend supports
the complete built-in catalog. See the [JSON guide](json.md),
[temporal guide](temporal.md), [collections guide](collections.md), and
[callback guide](callbacks.md) for other method families.

## Use the methods in a filter

```hff
name:lower:startswith("a")
name:contains("Ali", "lic")
name:matches("^A.*e$")
active:and(age:round>=18)
```

`contains()` requires at least one argument and checks that every supplied
substring occurs in the receiver. `matches()` searches for a matching substring;
use `^` and `$` when the whole string must match. HFF regex syntax follows RE2
except for the excluded `\C` byte escape, which is invalid and yields null.
`lower()` and `upper()` use locale-independent Unicode simple mappings and do
not normalize strings. `len()` counts Unicode code points for strings and bytes
for byte values.

For Int, `round()`, `ceil()`, and `floor()` leave the value unchanged. Float
`round()`, `ceil()`, and `floor()` follow the backend's `float.rounding`
Profile detail. Rounding methods preserve the input numeric subtype.
`string:bool()` accepts exactly `true` or `false`, and
`string:number()` accepts JSON number spelling; neither trims whitespace.
Invalid patterns or conversions yield null.

Float operations are approximate and follow the backend's `float.*` Profile
details. For example, the local backend uses Go `math.Round`, so a stored
Float value of `2.5` rounds to `3`; PostgreSQL uses its native Float rounding,
which rounds that midpoint to `2` on the supported platform. Parsing can also
differ: local `string:number()` accepts underflow such as `1e-400` as zero,
while PostgreSQL's Float profile rejects underflow as null. Query Float
literals are parsed by the core compiler before backend translation. Inspect
the built filter/query profile when these differences affect results.

## Regular-expression support

The local backend follows HFF's RE2 contract. PostgreSQL 15 and later supports
a smaller, literal-pattern subset for `~`, `!~`, and `string:matches()`:

```hff
name~"^A"
name:matches("^A")
name!~"^A"
```

The PostgreSQL subset accepts non-NUL ASCII literals and character classes
whose parsed ranges contain only non-NUL ASCII (including `\d`, `\s`, and
`\w`), alternation, concatenation, captures, `*`, `+`, `?`, repeats whose
explicit bounds are at most 255, and absolute text anchors. Dot, case folding,
multiline anchors, word boundaries, quantified anchors, and non-literal
patterns are rejected by `Build` when the regex must be translated. Ordinary
negated or Unicode classes usually introduce unsupported ranges. A valid
RE2 pattern outside that subset is a backend error. A malformed pattern yields
null under both `~` and `!~`; the operator does not turn an invalid pattern
into true. Null or no-value operator operands produce false, while
`matches()` propagates null or no-value. Prefix `!` follows ordinary HFF
boolean semantics on the method result. See the
[PostgreSQL regex subset](../backends/postgresql/README.md#regular-expressions).

The PostgreSQL backend lowers supported literal regex filters on native
String columns directly. Callback bodies and nullable value contexts retain
their usual semantics. See the
[PostgreSQL SQL-shape notes](../backends/postgresql/optimizations.md#emit-eligible-native-string-method-predicates-directly)
for exact examples and limits.

## Base64 and byte values

`string:frombase64()` decodes standard padded base64 into an immutable bytes
value. `bytes:base64()` encodes bytes with the standard alphabet and required
padding. `string:frombase64r()` and `bytes:base64r()` use the same standard
alphabet without padding; empty input represents empty bytes. Each decoder
accepts only its matching form and rejects URL-safe characters, embedded
CR/LF, and non-canonical pad bits. Invalid decoding returns null, and null
inputs propagate as null.
The PostgreSQL backend supports `bytes:len()`, `bytes:base64()`, and
`bytes:base64r()` for native `bytea` and computed Bytes values, plus both
decoders for supported string receivers. Its SQL profile also supports
schema-known Bytes in JSON/JSONB collections as canonical padded base64 strings.
A base64-looking `Any` string is not inferred to be Bytes, and a `String|Bytes`
union remains ambiguous. See the
[PostgreSQL bytea guide](../backends/postgresql/README.md#bytes-and-base64) and
[JSON Bytes guide](../backends/postgresql/json.md#bytes-in-json-documents).

Use the `r` methods when a protocol requires standard base64 without `=`
padding. For example, the bytes `01 02 03 04` encode as `AQIDBA` with
`base64r()` and `AQIDBA==` with `base64()`. The two decoders accept only their
matching form; neither method guesses whether padding was omitted.

The PostgreSQL JSON backend also supports the schema-known
`Bytes|Int|Float|Null` and `Bytes|Bool|Int|Float|Null` subsets in native JSON
and JSONB with numeric metadata when Int/Float identity is needed. The
category-disjoint `Bytes|Bool` and `Bytes|Bool|Null` subsets need no numeric
metadata. This allows Bytes guards, decoded-byte `len()`, membership,
serialization, and numeric operations on numeric values. Lists containing only
numbers can be sorted with stable Int/Float ties; a list containing Bytes or
another nonnumeric value yields ordinary null. The listed Bool variants are
also recoverable by JSON category. Bytes itself cannot be ordered. See the
[JSON union guide](../backends/postgresql/json.md#bytes-in-json-documents) and
[runnable union example](https://github.com/ornew/hff/tree/main/examples/sql-json-unions).

Assuming `encoded` is a string field, these predicates check the decoded byte
length and a decode/encode round trip:

```hff
encoded:frombase64:len=4
encoded:frombase64:base64=encoded
```

The methods charge their input byte length to the local per-match `MaxSteps`
budget. Run the complete example from the repository root:

```sh
go run ./examples/base64
```

It prints:

```text
"AQIDBA==" -> true
"AQIDBA" -> false
"AQIDBA==\n" -> false
```

The raw-encoding example checks local behavior and builds the corresponding
PostgreSQL predicate without connecting to a database:

```sh
go run ./examples/base64-raw
```

It prints:

```text
"AQIDBA" -> true
"AQIDBA==" -> false
"AQIDBB" -> false
PostgreSQL predicate built: true
Parameter count: 1
```

See the [local backend configuration](../backends/local/configuration.md) for limit behavior.

## Generic value methods

```hff
payload:exists
payload:type="string"
payload:in("open", "pending")
```

`exists()` checks that a value is present and non-null, so false, zero, and empty
strings are still present. `type()` returns the runtime type name, including
`"null"` for a null value. Comparing that name does not narrow another
expression; use an explicit type guard when a method call needs a narrower type.
`in()` requires at least one candidate and uses HFF value equality.

## Keep capabilities narrow with a type guard

```go
features := capabilities.Standard().Without(
    capabilities.Method("list.contains"),
)
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.Any("value"))),
    engine.WithCapabilities(features),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx, `value:[string]:contains("hff")`)
if err != nil {
    return err
}
filter, err := local.Build(ctx, program)
```

The explicit guard narrows the call to the string signature, so the query does
not require the disabled `list.contains` capability. A matching string is
preserved; a different runtime type yields internal no-value and therefore a
completed false predicate. Since local `list.contains` is now implemented, this
example demonstrates type-specific capability selection; the disabled method
still cannot be used by an unguarded list call. Without the guard, a call
through an `Any` receiver requires every statically applicable method
capability. Capability checks are compile-time checks, while backend support
is checked by `Build`.

Run the complete example from the repository root:

```sh
go run ./examples/scalar-methods
```

It prints:

```text
Scalar predicate: true
Guarded string: true
Guarded list: false
```
