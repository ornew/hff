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
float64. Literal values are parsed by the core compiler; runtime Float parsing
follows the selected backend's Profile. Remainder accepts only integer operands
and has the dividend's sign. For Int, `round()`, `ceil()`, and `floor()` are
identity operations. Float rounding follows the selected backend's Profile.
Rounding methods preserve the input numeric subtype. Correct numeric spelling
outside the selected type's range yields null; a negative integer literal is
range-checked as a signed value, so the int64 minimum is representable.
Float64 is an approximate numeric type. Its parsing, arithmetic, rounding,
aggregation, and formatting follow the selected backend's declared
compatibility `Profile`; Float results and text need not match across
backends. Decimal and exponent spellings select Float, while integer
spellings select Int. Invalid conversions and non-finite Float results yield
null. Positive and negative zero compare equal and are both falsy. Serialized
Float values retain a decimal point or exponent marker so their numeric
subtype remains Float; exact text and signed-zero preservation are
backend-profile behavior. Int remains exact signed 64-bit, and mixed
Int/Float equality and ordering compare exact represented values without
rounding an Int through Float first. See the backend's `float.*` Profile
details for its concrete Float model.
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
type is a compile-time error. For a union or `Any`, method signatures are
resolved against every statically applicable member and argument combination;
all such signatures must be enabled. An incompatible runtime type still yields
null when the operation itself is enabled. Use an explicit type guard to narrow
the receiver before a method call when only some union members should be
permitted.

Structural equality capabilities follow the same static-type rule. Equality,
inequality, membership, list containment, list uniqueness, and deduplication
(`distinct()`) require list or object equality permission only for composite
value families that can be compared; compatible nested contents are checked
recursively. `Any` and union types require every statically possible composite
permission. An explicit type guard narrows the possible types before capability
checking. See [capabilities](capabilities.md) and [evaluation](evaluation.md).

```text
schema: x has type String | Int
expression: x:len
meaning: if x is a string, return its length; otherwise, return null
```

Applicability is checked across receiver and argument type combinations, not
only the receiver. Every statically applicable signature on a union or `Any`
must be enabled; a capability-disabled candidate is a compile-time error even
when other candidates are enabled. At runtime, a value incompatible with every
applicable signature yields null. Result types combine applicable branch
results and null propagation. For a statically known receiver, an applicable
but disabled method is also a compile-time error.

Nullable types use their non-null members for domain checks; actual null values
follow the null rules. An expression whose only possible value is null may
participate in known built-in operations, including `null:size` and
`null+null`. A known call still requires all statically applicable method
capabilities to be enabled, then null propagates at runtime. For a receiver
that is only known to be null, every known candidate family is applicable
because no non-null receiver type selects one family. This does not
authorize unknown methods, wrong arity, or incompatible non-null arguments. If
no known method signature is applicable, compilation fails; any applicable but
disabled signature produces a capability error.

## Explicit type guards

The postfix form `value:[Type]` checks a value against a type constraint without
converting it. On a match, it returns the original value with the result type
narrowed to the intersection of its existing type and the constraint, retaining
the constraint's nullable members. On a mismatch, it produces internal
no-value.
The guard can be applied to a schema variable or a computed expression and can
be chained with access and method calls. A following call is resolved and
capability-checked against the narrowed type, not the original union or `Any`.

Type guards are useful when only selected runtime types should be allowed to
perform an operation. For example, `value:[string]:contains("x")` requires the
string containment capability; if `value` has another runtime type, the guard
produces no-value and the call does not run. Without the type guard, a call on a
union or `Any` requires permission for every statically applicable signature.
Type guards do not change the value or authorize a disabled method. For example,
`value:[object|null]:size` resolves the object family while retaining null in
the guarded result type; a null runtime value propagates null through the call.
The existing `type()` method remains string-valued and has no special flow
narrowing behavior. Only an explicit type guard narrows method candidates.

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
