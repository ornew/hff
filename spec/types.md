# Types and Schemas

HFF is type-safe. Every variable used by a filter MUST have a schema supplied
at compile time. The schema is a host integration contract, not text declared
inside the query.

## Value types

| Type family | Values |
|:--|:--|
| `bool` | `true`, `false` |
| `number` | Numeric values such as `10` and `1.23` |
| `string` | Text such as `"Alice"` |
| `list` | Ordered elements |
| `object` | Named fields or map entries |
| `bytes` | Byte sequences |
| `timestamp` | Time values |
| `duration` | Time intervals |
| `null` | A null value |

The schema example `String | Int` denotes a union of string and integer values.
The number family distinguishes signed 64-bit integers (`int64`) and IEEE
binary64 floating-point values (`float64`). `Int` belongs to the integer branch.
Mixed arithmetic promotes to float64. Mixed equality and ordering compare the
exact represented values without rounding an integer to float64 first.
Division always returns float64, including for two integer operands. Integer
overflow and non-finite floating-point results yield null.
Integer-spelled literals have type int64; a decimal point or exponent selects
float64. Remainder accepts only integer operands and has the dividend's sign.
`round()` rounds a halfway value away from zero. Rounding methods preserve the
input numeric subtype. Correct numeric spelling outside the selected type's
range yields null; a negative integer literal is range-checked as a signed value,
so the int64 minimum is representable.
Float64 parsing and arithmetic use IEEE 754 round-to-nearest, ties-to-even.
Subnormal results and underflow to signed zero are valid finite results. This
rounding rule is distinct from the user-visible `round()` method. Overflow and
non-finite results yield null. Positive and negative zero compare equal and are
both falsy; serialization preserves the float64 sign of zero.
Capitalized schema names here are explanatory, not a finalized public API.

## Objects and collections

An object is typed as either a struct with individually typed fields, or a map.
A struct exposes fields declared by its schema. A map allows data-dependent
keys; `labels.missing` evaluates to null when the key is absent.

The distinction matters: an unknown struct field is a schema problem, whereas
an absent map entry is ordinary runtime data. Maps have string keys and a
declared value type; lists have a declared element type. A heterogeneous list
literal infers the union of its element types. Nullable values use `T | null`.
Optional struct fields may be absent and evaluate to null. Absence is not stored
as a present null entry: presence tests, enumeration, size, serialization, and
structural equality inspect only actual entries.

Object literals infer struct types from their named values. An empty list has
element type `Never`, the empty type; it can conform to any expected element type
without admitting values of an unknown type. Context can supply an element type
for checking callbacks on an empty list. Never is a compiler type, not a host
value or a result of `type()`. A callback must still have valid syntax, resolved
names, arity, and enabled operations even when the receiver is empty.

String-key indexing and `get()` infer the union of declared field value types,
plus null for possible absence; a known constant key can retain its particular
field type. A map lookup retains its declared value type plus null. An unknown
struct field in dot notation is rejected, whereas dynamic lookup can return null.
Access through Any retains dynamic checking.

Before evaluation, the host binding layer validates required variables, required
struct fields, and their schema types. Required-value absence or schema mismatch
is an input-validation failure, not an ordinary null result. Nullable/optional
absence and missing map keys remain normal null-safe data cases.

## Union types and Any

Union types accept values from more than one type. `Any` is a predefined schema
type accepting any HFF value.

For a statically known receiver type, a method that cannot be called on that
type is a compile-time error. For a union or `Any`, where a call's applicability
depends on the actual value, an incompatible runtime type yields null.

```text
schema: x has type String | Int
expression: x:len()
meaning: if x is a string, return its length; otherwise, return null
```

Dynamic dispatch does not enable a method excluded by the capability
configuration or unsupported by the backend. A union operation is allowed when
at least one branch supports it with compatible arguments. Other runtime branches
yield null. Result types combine applicable branch results and null propagation.
Applicability is checked across receiver and argument type combinations, not
only the receiver. Nullable types use their non-null members for domain checks;
actual null values follow the null rules. An expression whose only possible value
is null may participate in known built-in operations, including `null:size()` and
`null+null`. This does not authorize unknown methods, wrong arity, disabled
features, or incompatible non-null arguments. If no non-null type combination is
applicable, compilation fails. Partially applicable Any/union combinations are
checked at runtime and yield null when incompatible.

Method arguments must match the declared signature; boolean truthiness is not
an arbitrary method-argument conversion.

Callback parameters have inferred types and lexical scope. A parameter may
shadow an outer schema variable or callback parameter. Nested callbacks may
capture outer variables that are not shadowed.

Unions and `Any` may require runtime checks. More precise types allow more
optimization, including removing redundant null checks. Optimization MUST
preserve observable filter results.

## Boolean contexts

The filter result, logical operators, and predicate joining interpret their
operands as booleans using the rules in
[evaluation](evaluation.md#implicit-boolean-conversion). This is a specific
contextual conversion, not a general permission to coerce arbitrary method
arguments or comparison operands.

## Fold accumulator inference

A fold's accumulator type includes the initial value and every possible
callback result. The inferred type must be closed under the callback: the
callback must type-check when given that accumulator type and the element type.
A null callback result becomes the next accumulator, rather than cancelling the
fold. Guarded no-value still propagates to the whole call. Recursive type growth
that cannot produce a supported finite schema is rejected during compilation.
