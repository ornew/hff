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

func foldProjectionProgram(t *testing.T, source string, methods ...hff.MethodDeclaration) (hff.Program, hff.Node) {
	t.Helper()
	caps := capabilities.Standard()
	for _, method := range methods {
		caps = caps.Allow(capabilities.Method(method.ID()))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Bytes("asset"), schema.String("name"))))), engine.WithCapabilities(caps), engine.WithMethods(methods...))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(source, err)
	}
	var fold hff.Node
	var visit func(hff.Node)
	visit = func(n hff.Node) {
		if fold.Valid() {
			return
		}
		if method, ok := n.Method(); ok && (method.Name() == "foldl" || method.Name() == "foldr") {
			fold = n
			return
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			visit(child)
		}
	}
	visit(p.Root())
	if !fold.Valid() {
		t.Fatal("missing fold")
	}
	return p, fold
}

func TestUnusedDeferredFoldInputUsesLexicalBindings(t *testing.T) {
	for _, tc := range []struct {
		fold string
		want bool
	}{
		{`foldl(0,(a,v)=>a+1)`, true},
		{`foldr(0,(v,a)=>a+1)`, true},
		{`foldl(0,(a,v)=>a+v:size)`, false},
		{`foldr(0,(v,a)=>a+v:size)`, false},
		{`foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v:size))`, false},
		{`foldl(0,(a,v)=>[1]:foldl(a,(b,v)=>b+v))`, true},
		{`foldl(0,(a,v)=>[1]:map(x=>v:type):size+a)`, false},
		{`foldl(0,(a,v)=>[1]:map(v=>v+1):size+a)`, true},
		{`foldl(0,(a,v)=>a+labels:size)`, true},
	} {
		p, n := foldProjectionProgram(t, `labels:items:map(p=>[p[1],p[1]]):`+tc.fold+`>0`)
		method, _ := n.Method()
		b := builder{ctx: context.Background(), program: p}
		got, err := b.unusedDeferredFoldInput(n, method.Name())
		if err != nil || got != tc.want {
			t.Fatal(tc.fold, got, err)
		}
		b.disableDeferredFoldPruning = true
		if got, err := b.unusedDeferredFoldInput(n, method.Name()); err != nil || got {
			t.Fatal("validation probe reentered pruning", tc.fold, got, err)
		}
	}
}

func TestUnusedDeferredFoldInputExcludesCustomRegions(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.fold.touch", Name: "touch", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:map(p=>[p[1],1:touch]):foldl(0,(a,v)=>a+1)>0`,
		`labels:items:map(p=>[p[1]]):foldl(0:touch,(a,v)=>a+1)>0`,
		`labels:items:map(p=>[p[1]]):foldl(0,(a,v)=>(a+1):touch)>0`,
	} {
		p, n := foldProjectionProgram(t, source, d)
		b := builder{ctx: context.Background(), program: p}
		if got, err := b.unusedDeferredFoldInput(n, "foldl"); err != nil || got {
			t.Fatal("custom region entered duplicate validation", source, got, err)
		}
	}
}

func TestUnusedDeferredFoldProbeKeepsEnclosingState(t *testing.T) {
	p, n := foldProjectionProgram(t, `labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+1)>0`)
	outer := jsonParameter{code: "outer.value", format: JSON, typ: schema.StringType}
	memo := &jsonItemRoleMemo{pairs: make(map[hff.Node]*jsonItemRoles), lists: make(map[hff.Node]*jsonItemRoles)}
	key := pathKey(hff.Path{"labels"})
	b := &builder{ctx: context.Background(), program: p, bindings: map[string]string{key: `"records"."labels"`}, jsonBindings: map[string]JSONFormat{key: JSON}, jsonValues: map[string]JSONFormat{}, reserved: map[string]bool{}, parameters: map[hff.BindingID]jsonParameter{777: outer}, steps: []string{"outer_step"}, args: []any{"outer"}, itemRoleMemo: memo}
	result, handled, err := b.jsonDeferredInputFold(n, "foldl")
	if err != nil || !handled || result.code == "" {
		t.Fatal(handled, result, err)
	}
	if b.disableDeferredFoldPruning || b.itemRoleMemo != memo || !reflect.DeepEqual(b.parameters, map[hff.BindingID]jsonParameter{777: outer}) {
		t.Fatal("validation policy or lexical environment leaked")
	}
	if b.steps[0] != "outer_step" || b.args[0] != "outer" {
		t.Fatal("enclosing projections or arguments discarded")
	}
	if strings.Contains(strings.Join(b.steps, " "), "json_build_array(json_build_array(") {
		t.Fatal("discarded packet work leaked from validation")
	}
}

func TestNestedUnusedDeferredFoldsKeepValidationBounded(t *testing.T) {
	body := "a+1"
	for depth := 1; depth <= 5; depth++ {
		if depth > 1 {
			body = `labels:items:map(p=>[p[1],p[1]]):foldl(a,(a,v)=>` + body + `)`
		}
		p, _ := foldProjectionProgram(t, `labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>`+body+`)>0`)
		ctx := &repeatCheckpointContext{Context: context.Background()}
		q, err := Build(ctx, p, WithDialect(PostgreSQL), WithBindings(BindJSON(hff.Path{"labels"}, Column("records", "labels"), JSON)))
		if err != nil || q == nil {
			t.Fatal(depth, err)
		}
		var count func(hff.Node) int
		count = func(n hff.Node) int {
			result := 1
			for i := 0; i < n.ChildCount(); i++ {
				child, _ := n.Child(i)
				result += count(child)
			}
			return result
		}
		nodes := count(p.Root())
		if ctx.checks > 200+6*nodes*depth {
			t.Fatal("nested validation replay exceeded its bounded workload", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}
