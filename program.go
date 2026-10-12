package hff

import (
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/plan"
)

// NodeKind identifies a semantic operation, independent from parser productions.
type NodeKind = plan.NodeKind

// Operator identifies arithmetic, comparison, and explicit NULL-test operations.
type Operator = plan.Operator

// BindingID is unique within a Program. IDs cannot be compared across programs.
type BindingID = plan.BindingID

// Binding describes a lexically scoped callback parameter or capture.
type Binding plan.Binding

// Signature is a resolved method implementation with receiver and argument types.
type Signature plan.Signature

// Method lists the applicable implementations of a resolved method call.
type Method plan.Method

// Node is an opaque immutable semantic node. Its zero value is invalid.
// Children are in evaluation order. Child and payload accessors return false
// for invalid indices or inapplicable payloads; returned slices are copies.
type Node plan.Node

// Program holds a typed plan and the schema, capabilities, and limits used to
// compile it. Compilation creates programs; backends read them without mutation.
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

// ID returns the binding identity, which is scoped to one Program.
func (b Binding) ID() BindingID { return plan.Binding(b).ID() }

// Name returns the callback parameter or capture name.
func (b Binding) Name() string { return plan.Binding(b).Name() }

// Type returns the binding's resolved type.
func (b Binding) Type() Type { return Type(plan.Binding(b).Type()) }

// ID returns the stable method signature identity.
func (s Signature) ID() string { return plan.Signature(s).ID() }

// Receiver returns the signature's receiver type.
func (s Signature) Receiver() Type { return Type(plan.Signature(s).Receiver()) }

// Parameters returns a defensive copy of the ordered declared parameter types:
// required parameters, optional parameters, then the repeated type if variadic.
func (s Signature) Parameters() []Type { return publicTypes(plan.Signature(s).Parameters()) }

// Result returns the signature's successful result type.
func (s Signature) Result() Type { return Type(plan.Signature(s).Result()) }

// Variadic reports whether the signature repeats its final parameter type.
func (s Signature) Variadic() bool { return plan.Signature(s).Variadic() }

// Name returns the resolved method call name.
func (m Method) Name() string { return plan.Method(m).Name() }

// Signatures returns a defensive copy of this call's applicable signatures.
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

// Kind returns the semantic operation represented by n.
func (n Node) Kind() NodeKind { return plan.Node(n).Kind() }

// Type returns the resolved result type of n.
func (n Node) Type() Type { return Type(plan.Node(n).Type()) }

// Span returns n's half-open UTF-8 byte range in Program.Source.
func (n Node) Span() Span { return plan.Node(n).Span() }

// MayNoValue reports whether a failed guard can produce internal no-value.
func (n Node) MayNoValue() bool { return plan.Node(n).MayNoValue() }

// ChildCount returns the number of children in source evaluation order.
func (n Node) ChildCount() int { return plan.Node(n).ChildCount() }

// Child returns child i and false when i is outside the child range.
func (n Node) Child(i int) (Node, bool) {
	child, ok := plan.Node(n).Child(i)
	return Node(child), ok
}

// Literal returns an explicit literal payload, including a Null value.
func (n Node) Literal() (Value, bool) {
	value, ok := plan.Node(n).Literal()
	return Value(value), ok
}

// Path returns a copy of the static path payload when n is a variable or field.
func (n Node) Path() ([]string, bool) { return plan.Node(n).Path() }

// Field returns a literal field name when n is a FieldNode.
func (n Node) Field() (string, bool) { return plan.Node(n).Field() }

// Operator returns the operation payload for binary and null-test nodes.
func (n Node) Operator() (Operator, bool) { return plan.Node(n).Operator() }

// Method returns the resolved method payload for a CallNode.
func (n Node) Method() (Method, bool) {
	method, ok := plan.Node(n).Method()
	return Method(method), ok
}

// Binding returns the referenced callback binding for a parameter node.
func (n Node) Binding() (Binding, bool) {
	binding, ok := plan.Node(n).Binding()
	return Binding(binding), ok
}

// Parameters returns callback parameters as a defensive copy.
func (n Node) Parameters() ([]Binding, bool) {
	bs, ok := plan.Node(n).Parameters()
	return publicBindings(bs), ok
}

// Captures returns callback captures as a defensive copy.
func (n Node) Captures() ([]Binding, bool) {
	bs, ok := plan.Node(n).Captures()
	return publicBindings(bs), ok
}

// Keys returns object-entry keys aligned with ObjectNode children.
func (n Node) Keys() ([]string, bool) { return plan.Node(n).Keys() }

// Constraint returns the declared runtime type test for a TypeGuardNode.
// Type may be narrower when the input schema already provides more information.
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

// Valid reports whether p is a compiler-created typed program.
func (p Program) Valid() bool { return plan.Program(p).Valid() }

// Root returns the root of the typed plan, or an invalid Node for invalid p.
func (p Program) Root() Node { return Node(plan.Program(p).Root()) }

// Source returns the original query string used to compile p.
func (p Program) Source() string { return plan.Program(p).Source() }

// Schema returns the immutable input schema used at compilation.
func (p Program) Schema() Type { return Type(plan.Program(p).Schema()) }

// Capabilities returns the capability policy used to compile p.
func (p Program) Capabilities() capabilities.Set { return plan.Program(p).Capabilities() }

// Limits returns the resolved stage limits used to compile p.
func (p Program) Limits() Limits { return plan.Program(p).Limits() }

// UnlimitedLimits reports whether compilation was explicitly configured
// without finite limits.
func (p Program) UnlimitedLimits() bool { return plan.Program(p).UnlimitedLimits() }

// Declaration returns the extension contract, or false for a built-in signature.
func (s Signature) Declaration() (MethodDeclaration, bool) {
	d, ok := plan.Signature(s).Declaration()
	return MethodDeclaration(d), ok
}
