// Typed plans are immutable and are constructed only by common compilation.
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

func (b Binding) ID() BindingID        { return plan.Binding(b).ID() }
func (b Binding) Name() string         { return plan.Binding(b).Name() }
func (b Binding) Type() Type           { return Type(plan.Binding(b).Type()) }
func (s Signature) ID() string         { return plan.Signature(s).ID() }
func (s Signature) Receiver() Type     { return Type(plan.Signature(s).Receiver()) }
func (s Signature) Parameters() []Type { return publicTypes(plan.Signature(s).Parameters()) }
func (s Signature) Result() Type       { return Type(plan.Signature(s).Result()) }
func (s Signature) Variadic() bool     { return plan.Signature(s).Variadic() }
func (m Method) Name() string          { return plan.Method(m).Name() }
func (m Method) Signatures() []Signature {
	signatures := plan.Method(m).Signatures()
	out := make([]Signature, len(signatures))
	for i, s := range signatures {
		out[i] = Signature(s)
	}
	return out
}
func (n Node) Valid() bool                { return plan.Node(n).Valid() }
func (n Node) Kind() NodeKind             { return plan.Node(n).Kind() }
func (n Node) Type() Type                 { return Type(plan.Node(n).Type()) }
func (n Node) Span() Span                 { return plan.Node(n).Span() }
func (n Node) MayNoValue() bool           { return plan.Node(n).MayNoValue() }
func (n Node) ChildCount() int            { return plan.Node(n).ChildCount() }
func (n Node) Child(i int) (Node, bool)   { child, ok := plan.Node(n).Child(i); return Node(child), ok }
func (n Node) Literal() (Value, bool)     { value, ok := plan.Node(n).Literal(); return Value(value), ok }
func (n Node) Path() ([]string, bool)     { return plan.Node(n).Path() }
func (n Node) Field() (string, bool)      { return plan.Node(n).Field() }
func (n Node) Operator() (Operator, bool) { return plan.Node(n).Operator() }
func (n Node) Method() (Method, bool)     { method, ok := plan.Node(n).Method(); return Method(method), ok }
func (n Node) Binding() (Binding, bool) {
	binding, ok := plan.Node(n).Binding()
	return Binding(binding), ok
}
func (n Node) Parameters() ([]Binding, bool) {
	bs, ok := plan.Node(n).Parameters()
	return publicBindings(bs), ok
}
func (n Node) Captures() ([]Binding, bool) {
	bs, ok := plan.Node(n).Captures()
	return publicBindings(bs), ok
}
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
func (p Program) Valid() bool                    { return plan.Program(p).Valid() }
func (p Program) Root() Node                     { return Node(plan.Program(p).Root()) }
func (p Program) Source() string                 { return plan.Program(p).Source() }
func (p Program) Schema() Type                   { return Type(plan.Program(p).Schema()) }
func (p Program) Capabilities() capabilities.Set { return plan.Program(p).Capabilities() }
func (p Program) Limits() Limits                 { return plan.Program(p).Limits() }
func (p Program) UnlimitedLimits() bool          { return plan.Program(p).UnlimitedLimits() }

// Declaration returns the extension contract, or false for a built-in signature.
func (s Signature) Declaration() (MethodDeclaration, bool) {
	d, ok := plan.Signature(s).Declaration()
	return MethodDeclaration(d), ok
}
