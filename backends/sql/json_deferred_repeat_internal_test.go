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
		groups, ok, err := b.deferredRepeatGroups(n)
		if err != nil || !ok || len(groups) != 1 {
			t.Fatal(body, ok, err)
		}
		count := len(groups[0].positions)
		probe := b.validationBuilder()
		want, err := probe.deferredValue(groups[0].first)
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

func TestRepeatedDeferredConstructorClassifiesExpressions(t *testing.T) {
	for _, body := range []string{`[]`, `[v]`, `[v,v!]`, `[v[0],v[0]]`, `[v:type,v:type]`, `[v]:concat([])`, `[v]:concat([v!])`, `[v]:concat([v]:map(v=>v))`} {
		p, n, id := repeatConstructor(t, body)
		b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.AnyType}}}
		_, handled, err := b.deferredRepeatGroups(n)
		want := body == `[v[0],v[0]]` || body == `[v:type,v:type]`
		if err != nil || handled != want {
			t.Fatal("incorrect constructor identity", body, handled, err)
		}
	}
}

func TestRepeatedDeferredExpressionKeysAreTypedAndOrdered(t *testing.T) {
	for _, tc := range []struct {
		left, right string
		same        bool
	}{
		{`v[1]`, `v[1]`, true}, {`v[1]`, `v[0]`, false}, {`v[1]`, `v[1.0]`, false},
		{`v[-1]`, `v[-1]`, true}, {`v[1]`, `v[-1]`, false},
		{`v[v:size-1]`, `v[v:size-1]`, true}, {`v[v:size-1]`, `v[v:size+1]`, false},
		{`v[1]!`, `v[1]!`, true}, {`v[1]!`, `v[1]`, false},
		{`v:[string]`, `v:[bytes]`, false}, {`v:[string]:lower`, `v:[string]:lower`, true},
		{`v[0]:len+1`, `1+v[0]:len`, false},
		{`v[0]:len+9007199254740992`, `v[0]:len+9007199254740993`, false},
		{`v[0]:len+0.0`, `v[0]:len+0e0`, true}, {`v[0]:len+0.0`, `v[0]:len+-0.0`, false},
		{`[v[1]]`, `[v[1]]`, true}, {`{"a":v,"b":v}`, `{"b":v,"a":v}`, false},
		{`v["a.b"]`, `v.a.b`, false},
		{`v:map(x=>x)`, `v:map(x=>x)`, false},
	} {
		p, n, id := repeatConstructor(t, `[`+tc.left+`,`+tc.right+`]`)
		b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.AnyType}}}
		left, _ := n.Child(0)
		right, _ := n.Child(1)
		lkey, _, err := b.deferredRepeatExpressionKey(left)
		if err != nil {
			t.Fatal(err)
		}
		rkey, _, err := b.deferredRepeatExpressionKey(right)
		if err != nil || (lkey != "" && lkey == rkey) != tc.same {
			t.Fatal(tc, lkey, rkey, err)
		}
	}
}

func TestRepeatedDeferredExpressionKeepsOrderDemand(t *testing.T) {
	p, n, id := repeatConstructor(t, `[v[1],v[1]]`)
	first, _ := n.Child(0)
	second, _ := n.Child(1)
	b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.AnyType}}}
	for _, marked := range []hff.Node{first, func() hff.Node { child, _ := first.Child(0); return child }()} {
		b.orderBlind = map[hff.Node]bool{marked: true}
		lkey, _, err := b.deferredRepeatExpressionKey(first)
		if err != nil {
			t.Fatal(err)
		}
		rkey, _, err := b.deferredRepeatExpressionKey(second)
		if err != nil || lkey == rkey {
			t.Fatal("different order demands shared", err)
		}
	}
}

func TestRepeatedDeferredGroupsRetainOriginalPositions(t *testing.T) {
	p, n, id := repeatConstructor(t, `["literal",v[1],[],v[0],v[1],null,v[0],1,1]`)
	b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.AnyType}}}
	groups, compressed, err := b.deferredRepeatGroups(n)
	if err != nil || !compressed {
		t.Fatal(compressed, err)
	}
	var positions [][]int
	for _, group := range groups {
		positions = append(positions, group.positions)
	}
	want := [][]int{{0}, {1, 4}, {2}, {3, 6}, {5}, {7}, {8}}
	if !reflect.DeepEqual(positions, want) {
		t.Fatal("mixed groups reordered, dropped data, or merged unbound literals", positions)
	}
	for _, body := range []string{`[v[1],v[1],v:map(x=>x)]`, `[v,v]:concat([])`} {
		p, n, id := repeatConstructor(t, body)
		b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.AnyType}}}
		_, compressed, err := b.deferredRepeatGroups(n)
		if err != nil || compressed {
			t.Fatal("unsupported constructor was partially shared", body, err)
		}
	}
}

func TestRepeatedDeferredLiteralViewKeepsElementPositions(t *testing.T) {
	p, n, id := repeatConstructor(t, `[v,v:type,v]`)
	b := builder{ctx: context.Background(), program: p, parameters: map[hff.BindingID]jsonParameter{id: {typ: schema.StringType, direct: &expression{code: "input.value", present: "TRUE", kind: hff.StringType}}}}
	plan, handled, err := b.repeatedDeferredList(n)
	if err != nil || !handled || plan.literal {
		t.Fatal(handled, plan, err)
	}
	positions, literal := plan.literalArmPositions()
	if !literal || !reflect.DeepEqual(positions, []int{0, 1, 0}) {
		t.Fatal("literal positions became compressed arm positions", positions, literal)
	}
	// The view is owned by its lexical constructor. A fresh row-plan must
	// not inherit it merely because it carries the same arm descriptors.
	rows := jsonDeferredList{arms: plan.arms}
	if _, literal := rows.literalArmPositions(); literal {
		t.Fatal("literal view leaked through row transformation")
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
