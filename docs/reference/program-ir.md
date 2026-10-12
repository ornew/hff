# Typed IR Reference

HFF compiles each query into an immutable, backend-independent typed program.
This reference describes the `Program` entry point and the node tree's types,
source spans, guards, callbacks, and payload accessors. Backends inspect this
IR without depending on parser nodes; a syntax tree cannot be evaluated or
translated to SQL.

Only HFF's compiler constructs valid programs and nodes. A node is an opaque
handle; callers cannot mutate compiler storage. Programs and their values are
safe to reuse concurrently.

## Program and node accessors

The generated Go Reference lists the exact signatures and source comments for
[`Program`](go/hff/#Program), [`Node`](go/hff/#Node),
[`Method`](go/hff/#Method), and [`Binding`](go/hff/#Binding). Use this page to
interpret those accessors as a semantic tree:

| Purpose | Accessors |
| --- | --- |
| Enter the compiled tree and inspect its configuration | `Program.Root()`, `Source()`, `Schema()`, `Capabilities()`, `Limits()`, `UnlimitedLimits()` |
| Identify a node's operation and result | `Node.Valid()`, `Kind()`, `Type()`, `Span()`, `MayNoValue()` |
| Traverse children in evaluation order | `ChildCount()`, `Child(i)` |
| Read literal, path, field, and operator payloads | `Literal()`, `Path()`, `Field()`, `Operator()` |
| Inspect a resolved call or a type guard | `Method()`, `Constraint()` |
| Inspect lexical scope and object entries | `Binding()`, `Parameters()`, `Captures()`, `Keys()` |

The zero Program and Node are invalid. A valid empty query has a MatchAll root.
`Child` returns false for an invalid index. A payload accessor returns false for
an inapplicable kind. Returned slices are copies; `Value`, `Type`, `Method`, and
`Binding` are immutable values.

`Program.Source` returns the original query. `Schema`, `Capabilities`, and
resource-limit accessors report the configuration used to compile it. `Span`
is the node's half-open UTF-8 byte interval in the source. `Type` is the resolved
result type, including explicit nullable union members. `MayNoValue` separately
reports whether a failed guard can yield internal no-value. No-value is not a
host-constructible value or schema type.

`Path` is available on variable and field nodes and returns the static literal
path as a copy; dynamic indices remain child nodes. `Field` gives the literal
field name on a field node. `Operator` gives the selected operator on binary
and null-test nodes. `Literal` returns explicit literals, including null.

## Node kinds and child order

`NodeKind` identifies these operations. The order of children and payloads is a
compatibility contract. Child order follows source evaluation order. Keep
existing kind values stable; append new kinds without renumbering or reusing
values.

| Kind | Children, in order | Payload accessors |
|:--|:--|:--|
| `InvalidNode` | None | None; zero value only |
| `MatchAllNode` | None | None |
| `LiteralNode` | None | `Literal` |
| `VariableNode` | None | `Path`, `Binding` |
| `ParameterNode` | None | `Binding` |
| `FieldNode` | Receiver | `Path`, `Field` |
| `IndexNode` | Receiver, index expression | None |
| `GuardNode` | Guarded value | None |
| `NullTestNode` | Tested value | `Operator` (`=` or `!=`) |
| `BinaryNode` | Left operand, right operand | `Operator` |
| `NegateNode` | Operand | None |
| `BoolNode` | Value | None |
| `AndNode` | Operands, left to right | None |
| `OrNode` | Operands, left to right | None |
| `NotNode` | Operand | None |
| `ListNode` | Elements in source order | None |
| `ObjectNode` | Entry values in source order | `Keys`, aligned by index with children |
| `CallNode` | Receiver, then arguments in source order | `Method` |
| `CallbackNode` | Body | `Parameters`, `Captures` |
| `TypeGuardNode` | Guarded value | `Constraint` |

A callback passed as a call argument occupies that argument's child position.
Its parameter and capture bindings are metadata; its body is its child. An
object's `Keys` slice aligns with the value children and is returned by copy.
Groups that only affect precedence do not need runtime nodes; their source
range remains represented by the compiled node span.

Backends must reject kinds and operators they do not support. Unknown kinds must
fail safely so an additive IR change cannot silently produce an incorrect
query. Optimization must preserve the child order and language behavior below.

## Methods and callback bindings

A `Method` exposes its call name and its declared signatures. Each `Signature`
contains a stable ID, receiver type, parameter types, result type, and variadic
flag. For a CallNode, `Method.Signatures()` contains only the signatures
statically applicable to that call, not the entire method registry. A concrete
receiver may have one applicable signature; an `Any` or union receiver may
have multiple family signatures for runtime dispatch. Every applicable
signature must be enabled or compilation fails with a capability error. A
`TypeGuardNode` narrows the receiver before method resolution, so only the
signatures applicable to the narrowed type are checked. A runtime value
incompatible with all applicable signatures yields null. A null-only receiver
has no non-null family to select a signature, so all known applicable
candidates must be enabled before null propagates at runtime. Each signature ID
identifies its concrete family and overload. Returned signatures and parameter
slices are copies. Backends use these IDs to dispatch without repeating common
type resolution; they must preserve required runtime checks for dynamic types.

`Constraint` returns the user-specified type expression attached to a
`TypeGuardNode`; it does not return the node's inferred result type. `Type()`
reports the intersection with the receiver's known type, preserving known
structural or element precision and nullable members. The runtime checker uses
the original constraint. On a match, the node preserves the original value; a
mismatch produces no-value and `MayNoValue()` is true. The accessor returns
false for other node kinds.

Each `Binding` exposes its `ID`, source `Name`, and inferred `Type`. Both root
variable nodes and callback parameter nodes return their binding through
`Binding()`. A binding ID is unique within one Program across root variables
and callback parameters, including nested and shadowed callbacks. Zero is
invalid, and a payload accessor returns false when the ID is zero. IDs from
different programs have no shared identity. Parameter bindings are listed in
declaration order. Captures identify outer bindings by their existing IDs, so
a backend need not resolve names or rebuild lexical scope.

## Preserve language behavior

The IR keeps explicit null tests, guards, boolean conversion, and logical
short-circuit nodes visible. `x=null` is a null test because the right side is
an explicit null literal; `x=(null)` has the same meaning. A comparison with an
expression that merely evaluates to null is an ordinary comparison. Optimizers
must retain this distinction.

A failed guard produces no-value, distinct from null. `MayNoValue` allows a
backend to see where that state can occur. Later access, comparisons, and calls
follow the propagation rules in the language specification; `exists()` is an
explicit presence check. AND and OR evaluate children left to right and skip
later children once the result is determined. Calls evaluate the receiver and
arguments in order; collections evaluate entries in source order. A backend
must preserve these semantics when lowering or optimizing a program.
