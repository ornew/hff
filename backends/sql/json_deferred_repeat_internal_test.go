package sql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func repeatConstructor(t *testing.T, body string) (hff.Program, hff.Node, hff.BindingID) {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):flatmap(v=>`+body+`):exists`)
	if err != nil {
		t.Fatal(err)
	}
	var constructor hff.Node
	var id hff.BindingID
	var visit func(hff.Node)
	visit = func(n hff.Node) {
		if builtinListCall(n, "flatmap") && !constructor.Valid() {
			callback, _ := n.Child(1)
			parameters, _ := callback.Parameters()
			id = parameters[0].ID()
			constructor, _ = callback.Child(0)
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			visit(child)
		}
	}
	visit(p.Root())
	if !constructor.Valid() {
		t.Fatal("missing constructor")
	}
	return p, constructor, id
}

func TestRepeatedDeferredConstructorKeepsDescriptorAndMultiplicity(t *testing.T) {
	for _, body := range []string{`[v,v]`, `[v,v,v]`, `[v]:concat([v])`, `[v,v]:concat([v])`, `[v]:concat([v]:concat([v,v]))`} {
		p, n, id := repeatConstructor(t, body)
		direct := expression{code: "outer.value", present: "outer.present", kind: hff.BytesType}
		b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{
			id: {typ: schema.BytesType, direct: &direct, lossy: true},
		}, args: []any{"kept"}, steps: []string{"outer"}}
		_, count, ok, err := b.repeatedDeferredParameter(n)
		if err != nil || !ok {
			t.Fatal(body, ok, err)
		}
		probe := b.validationBuilder()
		first, _, _, _ := probe.repeatedDeferredParameter(n)
		want, err := probe.deferredValue(first)
		if err != nil {
			t.Fatal(err)
		}
		plan, handled, err := b.repeatedDeferredList(n)
		if err != nil || !handled || plan.literal || len(plan.arms) != 1 {
			t.Fatal(body, plan, handled, err)
		}
		arm := plan.arms[0]
		if strings.Count(arm.from, "),(")+1 != count || len(arm.ordinal) != 1 || arm.empty || plan.null != "FALSE" {
			t.Fatal("wrong multiplicity", body, count, plan)
		}
		if !reflect.DeepEqual(arm.value, want) {
			t.Fatal("descriptor changed", body, arm.value)
		}
		if b.alias != 1 || len(b.args) != 1 || len(b.steps) != 1 || b.args[0] != "kept" {
			t.Fatal("discarded lowering state leaked", body, b.alias, b.args, b.steps)
		}
	}
}

func TestRepeatedDeferredConstructorRejectsNonidenticalValues(t *testing.T) {
	for _, body := range []string{`[]`, `[v]`, `[v,v!]`, `[v[0],v[0]]`, `[v:type,v:type]`, `[v]:concat([])`, `[v]:concat([v!])`, `[v]:concat([v]:map(v=>v))`} {
		p, n, id := repeatConstructor(t, body)
		b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.AnyType}}}
		_, handled, err := b.repeatedDeferredList(n)
		if err != nil || handled {
			t.Fatal("ineligible constructor shared", body, handled, err)
		}
	}
}

func TestRepeatedDeferredConstructorDistinguishesCapturedBindings(t *testing.T) {
	p, outer, outerID := repeatConstructor(t, `[v]:flatmap(u=>[u,v])`)
	callback, _ := outer.Child(1)
	parameters, _ := callback.Parameters()
	list, _ := callback.Child(0)
	// Both descriptors are deliberately identical. Only lexical identity is
	// sufficient: a capture and the current item may differ at runtime.
	value := jsonParameter{typ: schema.AnyType, code: "same.carrier", format: JSON}
	b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{
		outerID: value, parameters[0].ID(): value,
	}}
	_, handled, err := b.repeatedDeferredList(list)
	if err != nil || handled {
		t.Fatal("captured binding merged by descriptor equality", handled, err)
	}
}
