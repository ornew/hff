package plan_test

import (
	"sync"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/schema"
)

func TestPublicPlanSnapshots(t *testing.T) {
	typ := core.PrimitiveType(core.IntType)
	literal := plan.NewNode(plan.Spec{Kind: plan.LiteralNode, Type: typ, Literal: core.Int(10), Span: hff.Span{Start: 4, End: 6}})
	path := []string{"age"}
	ref := plan.NewNode(plan.Spec{Kind: plan.VariableNode, Type: typ, Path: path, Binding: plan.NewBinding(1, "age", typ), Span: hff.Span{Start: 0, End: 3}})
	children := []plan.Node{ref, literal}
	comparison := plan.NewNode(plan.Spec{Kind: plan.BinaryNode, Type: core.PrimitiveType(core.BoolType), Operator: plan.Greater, Children: children, Span: hff.Span{Start: 0, End: 6}})
	program := hff.Program(plan.NewProgram("age>10", comparison, core.Type(schema.Struct(schema.Int("age"))), capabilities.Standard(), hff.DefaultLimits(), false))
	path[0] = "modified"
	children[0] = literal
	root := program.Root()
	if !program.Valid() || root.Kind() != hff.BinaryNode || root.ChildCount() != 2 || program.Source() != "age>10" {
		t.Fatal("program lost its plan")
	}
	left, ok := root.Child(0)
	if !ok || left.Kind() != hff.VariableNode {
		t.Fatal("child storage was aliased")
	}
	got, _ := left.Path()
	got[0] = "modified"
	again, _ := left.Path()
	if again[0] != "age" {
		t.Fatal("path storage was aliased")
	}
	if binding, ok := left.Binding(); !ok || binding.ID() != 1 || binding.Name() != "age" {
		t.Fatal("schema binding identity lost")
	}
	if _, ok := root.Child(-1); ok {
		t.Fatal("negative child index")
	}
	if _, ok := root.Child(2); ok {
		t.Fatal("out of range child index")
	}
	if _, ok := root.Literal(); ok {
		t.Fatal("inapplicable literal payload")
	}
	op, ok := root.Operator()
	if !ok || op != hff.Greater {
		t.Fatal("operator lost")
	}
	right, _ := root.Child(1)
	value, ok := right.Literal()
	n, _ := value.AsInt()
	if !ok || n != 10 || right.Span() != (hff.Span{Start: 4, End: 6}) {
		t.Fatal("literal or byte span lost")
	}
	permissions := program.Capabilities().Without(capabilities.Comparison)
	if permissions.Has(capabilities.Comparison) || !program.Capabilities().Has(capabilities.Comparison) {
		t.Fatal("program capability storage was aliased")
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if n, ok := program.Root().Child(0); !ok || n.Kind() != hff.VariableNode {
					t.Error("concurrent plan read")
				}
			}
		})
	}
	wg.Wait()
}

func TestCallbackAndMethodMetadata(t *testing.T) {
	parameters := []core.Type{core.PrimitiveType(core.IntType)}
	function := core.NewFunctionType(parameters, core.PrimitiveType(core.BoolType))
	parameters[0] = core.PrimitiveType(core.StringType)
	if function.Parameters()[0].Kind() != hff.IntType || function.Result().Kind() != hff.BoolType || function.Check() == nil {
		t.Fatal("callback signature or host schema boundary")
	}
	copyTypes := function.Parameters()
	copyTypes[0] = core.PrimitiveType(core.StringType)
	if function.Parameters()[0].Kind() != hff.IntType {
		t.Fatal("mutable callback signature")
	}
	binding := plan.NewBinding(1, "x", core.PrimitiveType(core.IntType))
	capture := plan.NewBinding(2, "x", core.PrimitiveType(core.StringType))
	bindings := []plan.Binding{binding}
	captures := []plan.Binding{capture}
	body := plan.NewNode(plan.Spec{Kind: plan.ParameterNode, Type: core.PrimitiveType(core.IntType), Binding: binding})
	callback := plan.NewNode(plan.Spec{Kind: plan.CallbackNode, Type: function, Children: []plan.Node{body}, Parameters: bindings, Captures: captures})
	bindings[0] = capture
	captures[0] = binding
	got, _ := callback.Parameters()
	got[0] = capture
	again, _ := callback.Parameters()
	actualCapture, _ := callback.Captures()
	if again[0].ID() != 1 || actualCapture[0].ID() != 2 || again[0].Name() != "x" {
		t.Fatal("bindings or captures were aliased")
	}
	signature := plan.NewSignature("list.any", core.NewListType(core.PrimitiveType(core.IntType)), []core.Type{function}, core.PrimitiveType(core.BoolType), false)
	signatures := []plan.Signature{signature}
	method := plan.NewMethod("any", signatures...)
	signatures[0] = plan.Signature{}
	call := plan.NewNode(plan.Spec{Kind: plan.CallNode, Type: core.PrimitiveType(core.BoolType), Method: method, Children: []plan.Node{body, callback}})
	m, _ := call.Method()
	m.Signatures()[0] = plan.Signature{}
	s := m.Signatures()[0]
	p := s.Parameters()
	p[0] = core.PrimitiveType(core.StringType)
	if s.ID() != "list.any" || s.Receiver().Kind() != hff.ListType || s.Parameters()[0].Kind() != hff.FunctionType || s.Result().Kind() != hff.BoolType || s.Variadic() {
		t.Fatal("resolved signature was aliased")
	}
}

func TestSemanticBoundariesAndZeroValues(t *testing.T) {
	var node hff.Node
	var program hff.Program
	if node.Valid() || node.Kind() != hff.InvalidNode || node.ChildCount() != 0 || node.MayNoValue() || program.Valid() || program.Root().Valid() {
		t.Fatal("zero plan must be invalid")
	}
	if _, ok := node.Path(); ok {
		t.Fatal("zero path")
	}
	if _, ok := node.Method(); ok {
		t.Fatal("zero method")
	}
	if _, ok := node.Binding(); ok {
		t.Fatal("zero binding")
	}
	guard := plan.NewNode(plan.Spec{Kind: plan.GuardNode, Type: core.PrimitiveType(core.IntType), MayNoValue: true})
	nullTest := plan.NewNode(plan.Spec{Kind: plan.NullTestNode, Type: core.PrimitiveType(core.BoolType), Operator: plan.Equal, Children: []plan.Node{guard}})
	if !guard.MayNoValue() || nullTest.MayNoValue() || nullTest.Kind() == hff.BinaryNode {
		t.Fatal("guard/null-test distinctions lost")
	}
	keys := []string{"z", "a"}
	object := plan.NewNode(plan.Spec{Kind: plan.ObjectNode, Type: core.Type(schema.Struct()), Keys: keys})
	keys[0] = "modified"
	k, _ := object.Keys()
	k[0] = "modified"
	k, _ = object.Keys()
	if k[0] != "z" || k[1] != "a" {
		t.Fatal("source key order or immutable storage lost")
	}
	field := plan.NewNode(plan.Spec{Kind: plan.FieldNode, Field: "", Type: core.PrimitiveType(core.StringType)})
	if name, ok := field.Field(); !ok || name != "" {
		t.Fatal("empty field names are valid")
	}
	if _, ok := field.Path(); ok {
		t.Fatal("computed access has no schema-root path")
	}
}
