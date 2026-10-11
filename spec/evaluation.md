# Evaluation

Evaluation uses data supplied by the host and has no side effects. The result
of a complete filter is interpreted as a boolean. Null safety is intentional:
it trades some ability to distinguish the source of a null for simpler queries
and fewer runtime failures.

## Implicit boolean conversion

When a non-boolean value occurs in a boolean context, convert it by its runtime
type:

| Value type | True when |
|:--|:--|
| `bool` | The value is `true` |
| `number` | The value is non-null and not zero |
| `string` | The value is non-null and not empty |
| `list` | The value is non-null and contains at least one element |
| `object` | The value is non-null and contains at least one field or entry |
| `bytes` | The value is non-null and not empty |
| `duration` | The value is non-null and not zero |
| `timestamp` | The value is non-null, including the Unix epoch |
| `null` | Never |

Thus `null`, `0`, `""`, `false`, `{}`, and `[]` are falsy.

```hff
age name
```

This filter requires a non-null, nonzero age and a non-null, nonempty name.
An implementation may simplify these checks using schema knowledge while
preserving results. Empty bytes and zero durations are also falsy; the Unix
epoch is a valid timestamp and is truthy.

Prefix `!` negates the converted boolean. For a nullable boolean `flag`:

| `flag` | Boolean conversion | `!flag` |
|:--|:--|:--|
| `true` | `true` | `false` |
| `false` | `false` | `true` |
| `null` | `false` | `true` |

Therefore `!flag` means `!(flag!=null flag!=false)` when explicit null checks
have their intended meaning. It is equivalent to `flag=false` for a non-nullable
boolean, but that equivalence does not hold for a nullable boolean under the
comparison rule below.

## Null propagation

Null-safe access, ordinary receiver calls, and arithmetic propagate null:

| Subexpression | Result |
|:--|:--|
| `null_obj.foo` | `null` when `null_obj` is null |
| `labels.missing` | `null` when the map key is absent |
| `null:size` | `null` |
| `null+null` | `null` |

A propagated null is indistinguishable from an explicit null value by ordinary
value comparison. No implicit exception-handling expression is needed for these
operations. The methods `type()`, `tojson()`, and `tostring()` are exceptions:
on a null receiver, each returns the string `"null"`. `exists()` returns false
for null or the internal no-value state from a failed guard. Other methods
propagate no-value, except predicate callback conversion in any/all.
Ordinary methods propagate null
arguments. Membership methods `in(any...)` and `list:contains(any...)` can inspect a null
search argument; their receiver still follows ordinary null propagation.
`object:has(string)` checks key presence even when the stored value is null.

## Arithmetic and operator domains

Arithmetic and unary minus operate on numbers; remainder requires int64.
String concatenation and timestamp/duration operations use their named methods,
rather than arithmetic overloads. Regex operators require string operands.
Other statically incompatible combinations are rejected; applicable union or
Any operations use runtime checks and return null for incompatible combinations.

## Comparisons and null

Equality and inequality with an explicit null literal are null tests. All other
comparisons involving a null operand return false. This gives:

| Expression | Input | Result |
|:--|:--|:--|
| `x=null` | `x` is null | `true` |
| `x!=null` | `x` is null | `false` |
| `x!=null` | `x` is non-null | `true` |
| `x=true` | `x` is null | `false` |
| `x!=true` | `x` is null | `false` |
| `x~"A"` | `x` is null | `false` |
| `x!~"A"` | `x` is null | `false` |
| `!(x=true)` | `x` is null | `true` |

This makes `!(x=y)` and `x!=y` equivalent only for appropriate non-null
operands. The same distinction applies to `!~`. A null obtained by evaluating
another expression is not an explicit null test: if both `x` and `y` are null,
`x=y` is false. Recognize explicit null tests before optimization; constant
folding must not change a comparison into a different null-testing operation.
Grouping an explicit null literal does not change its status: `x=(null)` is
still an explicit null test. An expression that merely computes null is not.

RE2 matching uses `~` and `!~`, with the pattern on the right. These operators
and `matches()` search for a matching substring. Use anchors such as `^` and `$`
to require a full-string match. Invalid patterns yield null, including under
`!~`; non-match is not a way to turn an invalid pattern into true. HFF's
reference regular-expression syntax follows RE2 except that the byte escape
`\C` is excluded and is an invalid pattern, so it yields null. Backends may
support a smaller regex subset; they must publish those restrictions and reject
unsupported patterns rather than claim full HFF regex support.
Numeric subtypes compare by numeric value. Otherwise, equality compares values
of the same type. Lists and objects compare recursively by contents; stored null
members compare equal to stored null members. This structural rule does not
override top-level null comparisons. Lists compare length and elements in order.
Objects compare their actual key sets and associated values, independent of
struct/map schema representation. A missing key differs from a present null key.
Within structural comparisons, different value families are unequal; numeric
subtypes still compare numerically. Membership and uniqueness use this same
structural value equality, including null search values. Ordering supports numbers, strings,
timestamps, and durations. Statically incompatible comparisons are compile-time
errors; dynamically incompatible operands from `Any` or unions yield null.
Strings compare lexicographically by Unicode code point, without normalization.

## Guarded paths

Ordinary access cannot establish where a null originated:

```hff
nullable.nullvalue=null
```

This may match because `nullable` is null, even if its `nullvalue` field was
never reached. When the receiver is known to be non-null, `foo.nullvalue=null`
can test the field directly. At the top level, `nullvalue=null` tests the
variable's nullness without a parent path.

## Explicit type guards

The postfix expression `value:[Type]` checks a runtime value against a type
constraint and narrows the expression's static type. It never converts or
rewrites the value. A matching value is returned unchanged; a mismatch produces
internal no-value. A type guard on a value that is already no-value propagates
no-value. It can guard a schema value or a computed expression, and no-value
propagation follows the same rules as other guarded expressions.

For example, `value:[string]:contains("x")` calls string containment only when
the guarded value is a string. Otherwise the guard yields no-value, so the call
does not execute and the surrounding predicate converts to false. The existing
no-value exceptions remain in effect: `exists()` reports false, and `type()`,
`tojson()`, and `tostring()` may inspect the no-value receiver as specified in
[methods](methods.md). A guard
narrowing a union or `Any` lets compilation check subsequent methods against the
narrowed type. Without that guard, every statically applicable method signature
must be permitted by capabilities, even when a particular runtime value would
not use every signature. This is a compile-time permission rule; a disabled
candidate does not become null or a runtime capability error.

Explicitly guarding a nullable receiver removes the ambiguity:

```hff
nullable!=null nullable.nullvalue=null
```

Postfix `!` provides shorthand for those guards. In predicate position the
following expressions have the stated expansions:

```hff
n1!.n2!.n3!.nullvalue=null
```

```hff
n1!=null n1.n2!=null n1.n2.n3!=null n1.n2.n3.nullvalue=null
```

```hff
n1!.n2!.n3!.nullvalue:exists
```

```hff
n1!=null n1.n2!=null n1.n2.n3!=null n1.n2.n3.nullvalue!=null
```

A failed guard must make these guarded predicates false. Simply replacing a
failed guard with null would be incorrect for a subsequent `=null` comparison.
Guards can also occur in value expressions. A failed guard produces an internal
**no-value** state, distinct from null and inaccessible as a literal or host
value. Subsequent access, arithmetic, and method calls propagate no-value.
Comparisons involving no-value return false, including `=null` and `!=null`;
boolean conversion maps no-value to false.

A successful guard always returns the original value, in every expression
context. `x!` therefore retains normal truthiness when used as a predicate:
zero, false, and empty values remain falsy. Use `x:exists` to ask whether a
value is present and non-null. This method returns false for null and no-value,
and true for every other value, including zero, false, and empty collections.
It is an explicit exception to ordinary no-value propagation. Thus
`n1!.n2!.value:exists` preserves the path's presence checks without giving
postfix `!` a context-dependent result.

Prefix NOT applies after boolean conversion, just as for other values. Thus
`!(a!.b=null)` is true when the guard fails. Logical AND and OR combine the
converted booleans; a failed guard in one branch does not invalidate another
OR branch. No-value in an argument, list element, or object value propagates
to the containing call or collection. Elements and entries are never silently
dropped. No-value in a transformation or fold callback propagates to the entire
method.
Predicate callbacks in `any()` and `all()` instead apply boolean conversion,
so no-value is a false predicate result for that element.
Ordinary null callback results are retained by `map()`; `flatmap()` requires a
list result and yields null if a callback produces another value type.

## Field-related filters

HFF restricts queries to filters over schema-declared data. Each predicate's
comparison left operand, or its bare value expression, must depend on a
schema-declared variable. The check applies before constant optimization.

| Complete query | Accepted? | Reason |
|:--|:--|:--|
| `1+1` | No | Constant computation |
| `10<20` | No | Constant comparison |
| `10<age` | No | The left operand does not relate to a field |
| `age>10` | Yes | Field comparison |
| `age+10<10` | Yes | The left operand derives from a field |
| `age name` | Yes | Predicates over declared data |

Literals remain valid as operands, arguments, and collection contents. The
restriction must therefore be checked in the semantic compiler rather than by
removing literal expressions from the parser. Inner constant expressions are
allowed: `age>(10+2)`, `(10+age)<20`, and `list:map(x=>1)` are valid. A constant
predicate such as `true` does not become valid merely because another AND or OR
branch references a field. Callback bodies may produce constants; the containing
method receiver supplies field dependence. Walk the filter's AND, OR, and NOT
nodes through grouping and validate each leaf predicate. Once inside the value
expression of a leaf, comparisons in arguments, indices, literal members, and
callbacks do not start new field-dependence checks. For example, `flag=(1<2)` is
valid, while `age>10 | true` is not. Empty input remains the explicit
match-all exception.

## Other runtime failures

Invalid conversions (including JSON, base64, timestamp, and number parsing),
out-of-range or non-integer list indices, division by zero, integer overflow,
and non-finite floating-point results yield null. These rules also apply to
constant operands: optimization must not turn an evaluation failure into a
compile-time error. Syntax errors, statically invalid types, and unavailable
features remain compile-time errors.

Services configure resource limits, including query length, depth, evaluation
steps, and generated element counts. Exceeding a limit produces an explicit
evaluation-cancellation result, distinct from null and from a completed boolean
result. Partial evaluation must not be presented as a completed filter result.

## Evaluation order

AND and OR evaluate left-to-right and short-circuit once their boolean result
is determined. Unevaluated branches consume no evaluation budget. Ordinary
method calls evaluate their receiver and arguments left-to-right.
`bool:and(bool)` and `bool:or(bool)` may skip an argument when the boolean receiver
already determines the result, preserving ordinary null-receiver propagation.
Arithmetic operands, call arguments, and collection entries are evaluated
left-to-right. Once propagation determines the result, remaining evaluations
may be omitted; omitted expressions consume no evaluation budget. Null list
members and object values are ordinary data and do not themselves nullify a
collection. A no-value member does propagate, allowing evaluation of remaining
members to stop. Presence inspection and null-search methods retain their
explicit exceptions. For example, a no-value receiver skips an ordinary method's
arguments, but `exists()` consumes that receiver and returns false.

Method argument types remain strict; truthiness conversion belongs to logical
operators and predicate contexts, including any/all predicate callbacks.

A backend must preserve results and document the execution constraints it can
guarantee. It must not claim local-runtime step accounting or branch-evaluation
guarantees that its processing system cannot enforce.

## Presence and predicate examples

The following value examples distinguish presence from truthiness. The schema
must admit the stated inputs; required-input validation still applies.

| Expression | Input | Result |
|:--|:--|:--|
| `x` | `x=0` | false as a filter |
| `x!` | `x=0` | false as a filter; the guarded value is still 0 |
| `x:exists` | `x=0` | true |
| `x:exists` | `x=false` | true |
| `x:exists` | `x=null` | false |
| `parent!.value:exists` | `parent=null` | false |
| `items:any(x=>x.active)` | `items=[]` | false |
| `items:all(x=>x.active)` | `items=[]` | true |
| `items:any(x=>x!.active)` | `items=[null,{"active":true}]` | true |
| `items:all(x=>x!.active)` | `items=[null,{"active":true}]` | false |
