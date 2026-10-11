# Inspect a Compiled Program

HFF is designed to compile a query once into an immutable, backend-independent
program. Local and SQL builders can inspect that program without depending on
parser nodes. The public `Program` and `Node` types are being implemented; the
API below describes the intended inspection contract. Until compilation is
available, a syntax tree cannot be evaluated or translated to SQL.

Only HFF's compiler constructs valid programs and nodes. A node is an opaque
handle; callers cannot mutate compiler storage. Programs and their values are
safe to reuse concurrently.

## Program and node accessors

The planned public inspection surface is:

```go
func (p Program) Valid() bool
func (p Program) Root() Node
func (p Program) Source() string
func (p Program) Schema() Type
func (p Program) Capabilities() capabilities.Set
func (p Program) Limits() Limits
func (p Program) UnlimitedLimits() bool

func (n Node) Valid() bool
func (n Node) Kind() NodeKind
func (n Node) Type() Type
func (n Node) Span() Span
func (n Node) MayNoValue() bool
func (n Node) ChildCount() int
func (n Node) Child(i int) (Node, bool)
func (n Node) Literal() (Value, bool)
func (n Node) Path() (Path, bool)
func (n Node) Field() (string, bool)
func (n Node) Operator() (Operator, bool)
func (n Node) Method() (Method, bool)
func (n Node) Binding() (Binding, bool)
func (n Node) Parameters() ([]Binding, bool)
func (n Node) Captures() ([]Binding, bool)
func (n Node) Keys() ([]string, bool)
```

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
retain multiple family signatures for runtime dispatch. Each signature ID
identifies its concrete family and overload. Returned signatures and parameter
slices are copies. Backends use these IDs to dispatch without repeating common
type resolution; they must preserve required runtime checks for dynamic types.

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
