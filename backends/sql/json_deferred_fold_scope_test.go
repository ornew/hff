package sql

import (
	"context"
	"reflect"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestDeferredFoldOrdinaryProbePreservesEnclosingScope(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `labels:items:map(p=>p[1]):foldl(0,(acc,v)=>acc+v:len):exists`)
	if err != nil {
		t.Fatal(err)
	}
	var fold hff.Node
	var visit func(hff.Node)
	visit = func(n hff.Node) {
		if method, ok := n.Method(); ok && method.Name() == "foldl" {
			fold = n
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			visit(child)
		}
	}
	visit(p.Root())
	outer := jsonParameter{code: "outer.value", format: JSON, typ: schema.StringType}
	memo := &jsonItemRoleMemo{pairs: make(map[hff.Node]*jsonItemRoles), lists: make(map[hff.Node]*jsonItemRoles)}
	key := pathKey(hff.Path{"labels"})
	b := &builder{ctx: ctx, program: p, bindings: map[string]string{key: `"records"."labels"`}, jsonBindings: map[string]JSONFormat{key: JSON}, jsonValues: map[string]JSONFormat{}, reserved: map[string]bool{}, parameters: map[hff.BindingID]jsonParameter{777: outer}, steps: []string{"outer_step"}, itemRoleMemo: memo}
	result, handled, err := b.jsonDeferredInputFold(fold, "foldl")
	if err != nil || !handled || result.code == "" {
		t.Fatalf("probe: handled%v result%v err%v", handled, result, err)
	}
	if b.itemRoleMemo != memo {
		t.Fatal("ordinary probe replaced enclosing role memo")
	}
	if len(b.parameters) != 1 || !reflect.DeepEqual(b.parameters[777], outer) {
		t.Fatal("ordinary probe replaced enclosing parameters")
	}
	if len(b.steps) < 2 || b.steps[0] != "outer_step" {
		t.Fatal("ordinary probe discarded enclosing projections")
	}
}
