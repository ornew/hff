package hff

import (
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/plan"
)

// NodeKind identifies a semantic operation in the typed IR, independent from
// parser productions. Use it to select the applicable Node payload accessor.
type NodeKind = plan.NodeKind

// Operator identifies arithmetic, comparison, and explicit null-test
// operations. It is meaningful only for node kinds that carry an operator.
type Operator = plan.Operator

// BindingID identifies a callback parameter or capture within one Program.
// IDs are lexical plan-local handles and cannot be compared across Programs.
type BindingID = plan.BindingID

// Binding describes a lexically scoped callback parameter or capture. A nested
// callback may capture an outer binding; names can be shadowed, so use ID for
// identity within the Program.
type Binding plan.Binding

// Signature is one resolved method declaration applicable at a call site. A
// call may contain several non-ambiguous signatures for overload resolution.
type Signature plan.Signature

// Method lists the applicable signatures of a resolved method call. It is IR
// metadata, not a callable extension implementation.
type Method plan.Method

// Node is an opaque immutable semantic node. Its zero value is invalid.
// Children are in source evaluation order. Child and payload accessors return
// false for invalid indices, invalid nodes, or inapplicable payloads; returned
// slices are copies. Use Kind before reading kind-specific payloads.
type Node plan.Node

// Program holds the immutable typed plan and the schema, capability policy,
// method declarations, and resolved limits used to compile it. Compilation
// creates Programs; backends consume them without mutation. Its zero value is
// invalid.
type Program plan.Program

// Node and operator constants are stable IR discriminants. Append new values;
// do not renumber or reuse existing values.
const (
	InvalidNode   = plan.InvalidNode
	MatchAllNode  = plan.MatchAllNode
	LiteralNode   = plan.LiteralNode
	VariableNode  = plan.VariableNode
	ParameterNode = plan.ParameterNode
	FieldNode     = plan.FieldNode
	IndexNode     = plan.IndexNode
	GuardNode     = plan.GuardNode
	NullTestNode  = plan.NullTestNode
	BinaryNode    = plan.BinaryNode
	NegateNode    = plan.NegateNode
	BoolNode      = plan.BoolNode
	AndNode       = plan.AndNode
	OrNode        = plan.OrNode
	NotNode       = plan.NotNode
	ListNode      = plan.ListNode
	ObjectNode    = plan.ObjectNode
	CallNode      = plan.CallNode
	CallbackNode  = plan.CallbackNode
	TypeGuardNode = plan.TypeGuardNode
	Equal         = plan.Equal
	NotEqual      = plan.NotEqual
	Less          = plan.Less
	LessEqual     = plan.LessEqual
	Greater       = plan.Greater
	GreaterEqual  = plan.GreaterEqual
	Match         = plan.Match
	NotMatch      = plan.NotMatch
	Add           = plan.Add
	Subtract      = plan.Subtract
	Multiply      = plan.Multiply
	Divide        = plan.Divide
	Remainder     = plan.Remainder
)

// ID returns the binding identity, scoped to one Program. Do not persist it as
// a cross-program identifier; an invalid zero Binding has ID zero.
func (b Binding) ID() BindingID { return plan.Binding(b).ID() }

// Name returns the callback parameter or capture name, or an empty string for
// an invalid zero Binding.
func (b Binding) Name() string { return plan.Binding(b).Name() }

// Type returns the binding's resolved type descriptor. Invalid bindings return
// the invalid zero Type.
func (b Binding) Type() Type { return Type(plan.Binding(b).Type()) }

// ID returns the stable method signature identity, or an empty string for an
// invalid signature.
func (s Signature) ID() string { return plan.Signature(s).ID() }

// Receiver returns the signature's receiver type descriptor, or an invalid
// zero Type for an invalid signature.
func (s Signature) Receiver() Type { return Type(plan.Signature(s).Receiver()) }

// Parameters returns a defensive copy of the ordered declared parameter types:
// required parameters, optional parameters, then the repeated type if variadic.
// Optional positions are positional, and the final parameter is repeated only
// when Variadic reports true. An invalid signature returns nil.
func (s Signature) Parameters() []Type { return publicTypes(plan.Signature(s).Parameters()) }

// Result returns the signature's declared result type. Extension
// callbacks may also return ordinary null as a data failure. An invalid
// signature returns the invalid zero Type.
func (s Signature) Result() Type { return Type(plan.Signature(s).Result()) }

// Variadic reports whether the signature repeats its final parameter type. An
// invalid signature reports false.
func (s Signature) Variadic() bool { return plan.Signature(s).Variadic() }

// Name returns the resolved method call name, or an empty string when m is an
// invalid zero Method.
func (m Method) Name() string { return plan.Method(m).Name() }

// Signatures returns a defensive copy of the signatures that compile-time
// resolution found applicable to this call. The result describes dispatch
// choices; it does not contain backend function pointers. An invalid zero
// Method returns an empty slice.
func (m Method) Signatures() []Signature {
	signatures := plan.Method(m).Signatures()
	out := make([]Signature, len(signatures))
	for i, s := range signatures {
		out[i] = Signature(s)
	}
	return out
}

// Valid reports whether n is a compiler-created node rather than its zero value.
func (n Node) Valid() bool { return plan.Node(n).Valid() }

// Kind returns the semantic operation represented by n, or InvalidNode for an
// invalid zero Node.
func (n Node) Kind() NodeKind { return plan.Node(n).Kind() }

// Type returns the statically resolved result type of n. A runtime type guard
// can narrow it relative to the original schema; an invalid Node returns the
// invalid zero Type.
func (n Node) Type() Type { return Type(plan.Node(n).Type()) }

// Span returns n's half-open UTF-8 byte range in Program.Source. An invalid
// Node returns the zero Span.
func (n Node) Span() Span { return plan.Node(n).Span() }

// MayNoValue reports whether n can produce the internal no-value state, such as
// when a postfix guard finds a missing or mismatched path. No-value is not an
// HFF Value and propagates through evaluation. An invalid Node reports false.
func (n Node) MayNoValue() bool { return plan.Node(n).MayNoValue() }

// ChildCount returns the number of children in source evaluation order. An
// invalid node has no children and returns zero.
func (n Node) ChildCount() int { return plan.Node(n).ChildCount() }

// Child returns child i and false when i is outside the child range.
func (n Node) Child(i int) (Node, bool) {
	child, ok := plan.Node(n).Child(i)
	return Node(child), ok
}

// Literal returns an explicit literal payload, including an explicit Null,
// and true only for LiteralNode. It returns an invalid zero Value and false
// for other node kinds or an invalid node.
func (n Node) Literal() (Value, bool) {
	value, ok := plan.Node(n).Literal()
	return Value(value), ok
}

// Path returns a copy of the static path from the Program's input root when n
// is a variable or field chain rooted there. Callback-local variables,
// dynamic indexing, and other node kinds have no such path and return false.
// Empty literal path components are preserved. A path with no components
// is not returned as a payload.
func (n Node) Path() ([]string, bool) { return plan.Node(n).Path() }

// Field returns the immediate literal field name and true only when n is a
// FieldNode. Empty names and names containing dots are returned unchanged.
func (n Node) Field() (string, bool) { return plan.Node(n).Field() }

// Operator returns the operation payload for binary and null-test nodes. The
// bool is false for other node kinds or an invalid node.
func (n Node) Operator() (Operator, bool) { return plan.Node(n).Operator() }

// Method returns the resolved method payload and true only for CallNode.
func (n Node) Method() (Method, bool) {
	method, ok := plan.Node(n).Method()
	return Method(method), ok
}

// Binding returns the referenced lexical binding and true for a ParameterNode
// or a VariableNode that names a callback binding. Input-root paths and other
// node kinds return false.
func (n Node) Binding() (Binding, bool) {
	binding, ok := plan.Node(n).Binding()
	return Binding(binding), ok
}

// Parameters returns callback parameters as a defensive copy and true only for
// CallbackNode. The bool distinguishes an empty parameter list from an
// inapplicable accessor.
func (n Node) Parameters() ([]Binding, bool) {
	bs, ok := plan.Node(n).Parameters()
	return publicBindings(bs), ok
}

// Captures returns callback captures as a defensive copy and true only for
// CallbackNode. Captures refer to lexical bindings outside that callback.
func (n Node) Captures() ([]Binding, bool) {
	bs, ok := plan.Node(n).Captures()
	return publicBindings(bs), ok
}

// Keys returns a copy of the object-entry keys aligned by index with
// ObjectNode children. It returns true for an object literal, including one
// with no entries, and false for other kinds or an invalid node.
func (n Node) Keys() ([]string, bool) { return plan.Node(n).Keys() }

// Constraint returns the declared runtime type test and true only for
// TypeGuardNode. The node's resolved Type may be narrower when the input schema
// already provides more information.
func (n Node) Constraint() (Type, bool) { typ, ok := plan.Node(n).Constraint(); return Type(typ), ok }
func publicBindings(bs []plan.Binding) []Binding {
	if bs == nil {
		return nil
	}
	out := make([]Binding, len(bs))
	for i, b := range bs {
		out[i] = Binding(b)
	}
	return out
}

// Valid reports whether p is a compiler-created typed program rather than the
// invalid zero Program.
func (p Program) Valid() bool { return plan.Program(p).Valid() }

// Root returns the root of the typed plan, or an invalid Node for an invalid
// zero Program.
func (p Program) Root() Node { return Node(plan.Program(p).Root()) }

// Source returns the original query text used to compile p, preserving source
// spelling for diagnostics and inspection. The zero Program returns an empty
// string.
func (p Program) Source() string { return plan.Program(p).Source() }

// Schema returns the exact immutable input schema used at compilation. The
// zero Program returns an invalid Type.
func (p Program) Schema() Type { return Type(plan.Program(p).Schema()) }

// Capabilities returns the explicit capability policy used to compile p. This
// is the compile-time permission set, not a backend support profile. An invalid
// zero Program returns the unspecified zero Set.
func (p Program) Capabilities() capabilities.Set { return plan.Program(p).Capabilities() }

// Limits returns the resolved limit values used to compile p. Inspect the
// compile-stage fields rather than interpreting zero as unlimited; zero fields
// were resolved from defaults unless UnlimitedLimits reports true. The zero
// Program returns the zero Limits value.
func (p Program) Limits() Limits { return plan.Program(p).Limits() }

// UnlimitedLimits reports whether compilation was explicitly configured
// without finite limits. It concerns compilation only, not input validation or
// backend runtime limits.
func (p Program) UnlimitedLimits() bool { return plan.Program(p).UnlimitedLimits() }

// Declaration returns the immutable extension declaration for an extension
// signature. The bool is false for built-in signatures or an invalid
// signature; a false result does not indicate a missing backend implementation.
func (s Signature) Declaration() (MethodDeclaration, bool) {
	d, ok := plan.Signature(s).Declaration()
	return MethodDeclaration(d), ok
}
