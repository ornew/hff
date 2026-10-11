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

`round()` rounds halfway cases away from zero: `2.5` becomes `3` and `-2.5`
becomes `-3`. `round()`, `ceil()`, and `floor()` preserve the input numeric
subtype. `string:bool()` accepts exactly `true` or `false`, and `string:number()`
accepts JSON number spelling; neither trims whitespace. Invalid patterns or
conversions yield null.

## Base64 and byte values

`string:frombase64()` decodes standard padded base64 into an immutable bytes
value. `bytes:tobase64()` encodes bytes with the standard alphabet and required
padding. URL-safe and unpadded forms, embedded CR/LF, and non-canonical pad bits
are rejected; an invalid decode returns null. Null inputs propagate as null.

Assuming `encoded` is a string field, these predicates check the decoded byte
length and a decode/encode round trip:

```hff
encoded:frombase64:len=4
encoded:frombase64:tobase64=encoded
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
