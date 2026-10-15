package sql

import (
	"context"
	"math"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
	"github.com/ornew/hff/schema"
)

func TestDeferredRepeatLiteralIdentityPreservesFloatBits(t *testing.T) {
	b := builder{ctx: context.Background()}
	key := func(value core.Value) string {
		n := hff.Node(plan.NewNode(plan.Spec{Kind: plan.LiteralNode, Type: core.PrimitiveType(core.FloatType), Literal: value}))
		k, _, err := b.deferredRepeatExpressionKey(n)
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	positive, negative := key(core.Float(0)), key(core.Float(math.Copysign(0, -1)))
	if positive == "" || negative == "" || positive == negative || key(core.Float(0)) != positive {
		t.Fatal("signed zero literal identities collapsed")
	}
}

func TestDeferredRepeatDoesNotInferExtensionPurity(t *testing.T) {
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "example.repeat.echo", Name: "echo", Receiver: schema.AnyType, Result: schema.AnyType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.AnyType))), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))), engine.WithMethods(declaration))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:flatmap(p=>[p[1]:echo,p[1]:echo]):exists`)
	if err != nil {
		t.Fatal(err)
	}
	b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{}}
	var calls []hff.Node
	var visit func(hff.Node)
	visit = func(n hff.Node) {
		if binding, ok := n.Binding(); ok {
			b.parameters[binding.ID()] = jsonParameter{typ: binding.Type()}
		}
		if method, ok := n.Method(); ok && method.Name() == "echo" {
			calls = append(calls, n)
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			visit(child)
		}
	}
	visit(p.Root())
	if len(calls) != 2 {
		t.Fatal("missing extension calls")
	}
	for _, call := range calls {
		key, _, err := b.deferredRepeatExpressionKey(call)
		if err != nil || key != "" {
			t.Fatal("custom signature entered builtin identity proof", key, err)
		}
	}
	p, err = e.Compile(context.Background(), `labels:items:flatmap(p=>[p[1],p[1],p[1]:echo]):exists`)
	if err != nil {
		t.Fatal(err)
	}
	b.program = p
	visit(p.Root())
	var inspect func(hff.Node)
	inspect = func(n hff.Node) {
		if n.Kind() == hff.ListNode {
			if _, compressed, err := b.deferredRepeatGroups(n); err != nil || compressed {
				t.Fatal("extension sibling did not retain the whole original path", err)
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			inspect(child)
		}
	}
	inspect(p.Root())
}

type repeatCheckpointContext struct {
	context.Context
	checks int
}

func (c *repeatCheckpointContext) Err() error {
	c.checks++
	return c.Context.Err()
}

func TestNestedDeferredRepeatValidationIsLinearInInputTree(t *testing.T) {
	body := "v"
	for depth := 1; depth <= 6; depth++ {
		body = "[" + body + "," + body + "]"
		p, n, id := repeatConstructor(t, body)
		ctx := &repeatCheckpointContext{Context: context.Background()}
		direct := expression{code: "outer.value", present: "TRUE", kind: hff.BytesType}
		b := builder{ctx: ctx, program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.BytesType, direct: &direct}}, jsonValues: map[string]JSONFormat{}}
		if _, _, err := b.repeatedDeferredList(n); err != nil {
			t.Fatal(err)
		}
		var count func(hff.Node) int
		count = func(n hff.Node) int {
			total := 1
			for i := 0; i < n.ChildCount(); i++ {
				child, _ := n.Child(i)
				total += count(child)
			}
			return total
		}
		nodes := count(n)
		key, _, err := b.deferredRepeatExpressionKey(n)
		if err != nil || len(key) > 512*nodes {
			t.Fatal("identity encoding expanded beyond its input tree", depth, len(key), nodes, err)
		}
		if b.disableDeferredRepeat || ctx.checks > 24*nodes {
			t.Fatal("recursive validation multiplied work or leaked its policy", depth, ctx.checks, nodes)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d keybytes=%d", depth, nodes, ctx.checks, len(key))
	}
}
