// Package plan owns immutable typed plans. Only the HFF compiler constructs them.
package plan

import (
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
)

type NodeKind uint8

const (
	InvalidNode NodeKind = iota
	MatchAllNode
	LiteralNode
	VariableNode
	ParameterNode
	FieldNode
	IndexNode
	GuardNode
	NullTestNode
	BinaryNode
	NegateNode
	BoolNode
	AndNode
	OrNode
	NotNode
	ListNode
	ObjectNode
	CallNode
	CallbackNode
	TypeGuardNode
)

func (k NodeKind) String() string {
	names := [...]string{"invalid", "match-all", "literal", "variable", "parameter", "field", "index", "guard", "null-test", "binary", "negate", "bool", "and", "or", "not", "list", "object", "call", "callback", "type-guard"}
	if int(k) >= len(names) {
		return "invalid"
	}
	return names[k]
}

type Operator string

const (
	Equal        Operator = "="
	NotEqual     Operator = "!="
	Less         Operator = "<"
	LessEqual    Operator = "<="
	Greater      Operator = ">"
	GreaterEqual Operator = ">="
	Match        Operator = "~"
	NotMatch     Operator = "!~"
	Add          Operator = "+"
	Subtract     Operator = "-"
	Multiply     Operator = "*"
	Divide       Operator = "/"
	Remainder    Operator = "%"
)

// BindingID is unique within one Program, including nested and shadowed bindings.
// Zero is invalid; IDs from different programs cannot be compared meaningfully.
type BindingID uint64
type Binding struct {
	id   BindingID
	name string
	typ  core.Type
}

func NewBinding(id BindingID, name string, typ core.Type) Binding { return Binding{id, name, typ} }
func (b Binding) ID() BindingID                                   { return b.id }
func (b Binding) Name() string                                    { return b.name }
func (b Binding) Type() core.Type                                 { return b.typ }

// Signature identifies one applicable runtime implementation of a method call.
type Signature struct {
	id         string
	receiver   core.Type
	parameters []core.Type
	result     core.Type
	variadic   bool
}

func NewSignature(id string, receiver core.Type, parameters []core.Type, result core.Type, variadic bool) Signature {
	return Signature{id, receiver, append([]core.Type(nil), parameters...), result, variadic}
}
func (s Signature) ID() string              { return s.id }
func (s Signature) Receiver() core.Type     { return s.receiver }
func (s Signature) Parameters() []core.Type { return append([]core.Type(nil), s.parameters...) }

// ParametersView returns immutable internal storage. Callers must not modify it.
func (s Signature) ParametersView() []core.Type { return s.parameters }
func (s Signature) Result() core.Type           { return s.result }
func (s Signature) Variadic() bool              { return s.variadic }

type Method struct {
	name       string
	signatures []Signature
}

func NewMethod(name string, signatures ...Signature) Method {
	return Method{name, append([]Signature(nil), signatures...)}
}
func (m Method) Name() string            { return m.name }
func (m Method) Signatures() []Signature { return append([]Signature(nil), m.signatures...) }

// SignaturesView is an internal read-only view; callers must not mutate it.
func (m Method) SignaturesView() []Signature { return m.signatures }

// Spec is internal compiler storage, never exposed by the public hff package.
type Spec struct {
	Kind       NodeKind
	Type       core.Type
	Span       core.Span
	MayNoValue bool
	Children   []Node
	Literal    core.Value
	Path       []string
	Field      string
	Operator   Operator
	Method     Method
	Binding    Binding
	Parameters []Binding
	Captures   []Binding
	Keys       []string
	Constraint core.Type
}

// Node has ordered children according to Kind:
//   - Variable, Parameter, Literal, MatchAll: none.
//   - Field, Guard, TypeGuard, NullTest, Negate, Bool, Not: receiver or operand.
//   - Index, Binary: receiver/index or left/right.
//   - And, Or, List, Object: source order (Object.Keys parallels children).
//   - Call: receiver first, then arguments in source order.
//   - Callback: body; parameters and captures are separate metadata.
//
// A Field has a Path only when rooted in the input schema. Field always returns
// its immediate field name, including empty names and names containing dots.
type Node struct{ data *Spec }

func NewNode(spec Spec) Node {
	spec.Children = append([]Node(nil), spec.Children...)
	spec.Path = append([]string(nil), spec.Path...)
	spec.Parameters = append([]Binding(nil), spec.Parameters...)
	spec.Captures = append([]Binding(nil), spec.Captures...)
	spec.Keys = append([]string(nil), spec.Keys...)
	return Node{&spec}
}
func (n Node) Valid() bool { return n.data != nil && n.data.Kind != InvalidNode }
func (n Node) Kind() NodeKind {
	if n.data == nil {
		return InvalidNode
	}
	return n.data.Kind
}
func (n Node) Type() core.Type {
	if n.data == nil {
		return core.Type{}
	}
	return n.data.Type
}
func (n Node) Span() core.Span {
	if n.data == nil {
		return core.Span{}
	}
	return n.data.Span
}
func (n Node) MayNoValue() bool { return n.data != nil && n.data.MayNoValue }
func (n Node) ChildCount() int {
	if n.data == nil {
		return 0
	}
	return len(n.data.Children)
}
func (n Node) Child(i int) (Node, bool) {
	if i < 0 || i >= n.ChildCount() {
		return Node{}, false
	}
	return n.data.Children[i], true
}
func (n Node) Literal() (core.Value, bool) {
	if n.Kind() != LiteralNode {
		return core.Value{}, false
	}
	return n.data.Literal, true
}
func (n Node) Path() ([]string, bool) {
	if (n.Kind() != VariableNode && n.Kind() != FieldNode) || len(n.data.Path) == 0 {
		return nil, false
	}
	return append([]string(nil), n.data.Path...), true
}
func (n Node) Field() (string, bool) {
	if n.Kind() != FieldNode {
		return "", false
	}
	return n.data.Field, true
}
func (n Node) Operator() (Operator, bool) {
	if n.Kind() != BinaryNode && n.Kind() != NullTestNode {
		return "", false
	}
	return n.data.Operator, true
}
func (n Node) Method() (Method, bool) {
	if n.Kind() != CallNode {
		return Method{}, false
	}
	return n.data.Method, true
}
func (n Node) Binding() (Binding, bool) {
	if (n.Kind() != ParameterNode && n.Kind() != VariableNode) || n.data.Binding.ID() == 0 {
		return Binding{}, false
	}
	return n.data.Binding, true
}
func (n Node) Parameters() ([]Binding, bool) {
	if n.Kind() != CallbackNode {
		return nil, false
	}
	return append([]Binding(nil), n.data.Parameters...), true
}

// ParametersView exposes immutable internal bindings without copying.
func (n Node) ParametersView() ([]Binding, bool) {
	if n.Kind() != CallbackNode {
		return nil, false
	}
	return n.data.Parameters, true
}
func (n Node) Captures() ([]Binding, bool) {
	if n.Kind() != CallbackNode {
		return nil, false
	}
	return append([]Binding(nil), n.data.Captures...), true
}
func (n Node) Keys() ([]string, bool) {
	if n.Kind() != ObjectNode {
		return nil, false
	}
	return append([]string(nil), n.data.Keys...), true
}
func (n Node) Constraint() (core.Type, bool) {
	if n.Kind() != TypeGuardNode {
		return core.Type{}, false
	}
	return n.data.Constraint, true
}

type Program struct{ data *program }
type program struct {
	source       string
	root         Node
	schema       core.Type
	capabilities capabilities.Set
	limits       core.Limits
	unlimited    bool
}

func NewProgram(source string, root Node, schema core.Type, permissions capabilities.Set, limits core.Limits, unlimited bool) Program {
	return Program{&program{source, root, schema, permissions, limits, unlimited}}
}
func (p Program) Valid() bool { return p.data != nil && p.data.root.Valid() }
func (p Program) Root() Node {
	if p.data == nil {
		return Node{}
	}
	return p.data.root
}
func (p Program) Source() string {
	if p.data == nil {
		return ""
	}
	return p.data.source
}
func (p Program) Schema() core.Type {
	if p.data == nil {
		return core.Type{}
	}
	return p.data.schema
}
func (p Program) Capabilities() capabilities.Set {
	if p.data == nil {
		return capabilities.Set{}
	}
	return p.data.capabilities
}
func (p Program) Limits() core.Limits {
	if p.data == nil {
		return core.Limits{}
	}
	return p.data.limits
}
func (p Program) UnlimitedLimits() bool { return p.data != nil && p.data.unlimited }
