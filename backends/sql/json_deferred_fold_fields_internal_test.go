package sql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func TestDeferredFoldFieldObservationUsesLexicalBindings(t *testing.T) {
	for _, tc := range []struct {
		body string
		want map[string]bool
	}{
		{`a+v.a:string:len`, map[string]bool{"a": true}},
		{`a+v["a"]:string:len`, map[string]bool{"a": true}},
		{`a+v.a:string:len+v.b:size`, map[string]bool{"a": true, "b": true}},
		{`[1]:foldl(a,(b,x)=>b+v.a:string:len)`, map[string]bool{"a": true}},
		{`a+v.a:string:len+[1]:foldl(0,(b,v)=>b+v)`, map[string]bool{"a": true}},
		{`a+v.a!:string:len`, map[string]bool{"a": true}},
		{`a+v:type:len`, nil},
		{`a+v!:size`, nil},
		{`a+v[labels.name]:string:len`, nil},
		{`a+v!.a:string:len`, nil},
		{`a+v.a:string:len+[1]:map(x=>v:type):size`, nil},
	} {
		p, fold := foldProjectionProgram(t, `labels:items:map(p=>{"a":p[1],"b":[p[1]],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>`+tc.body+`)>0`)
		b := builder{ctx: context.Background(), program: p}
		got, err := b.observedDeferredFoldFields(fold, "foldl")
		if err != nil || !reflect.DeepEqual(got, tc.want) {
			t.Fatal(tc.body, got, tc.want, err)
		}
		b.disableDeferredFoldPruning = true
		if got, err := b.observedDeferredFoldFields(fold, "foldl"); got != nil || err != nil {
			t.Fatal("validation reentered field pruning", got, err)
		}
	}
}

func TestDeferredFoldObjectPacketKeepsDescriptorMetadata(t *testing.T) {
	p, _ := foldProjectionProgram(t, `labels:items:foldl(0,(a,p)=>a+1)>0`)
	kept := deferredLeaf(jsonParameter{typ: schema.BytesType, code: "source.kept", format: JSON, lossy: true})
	discarded := &jsonDeferredValue{typ: hff.NewStructType(hff.Field{Name: "inner", Type: kept.typ}), present: "source.present", object: &[]jsonDeferredField{{"inner", kept}}}
	fields := []jsonDeferredField{{"used", kept}, {"unused", discarded}}
	typ := hff.NewStructType(hff.Field{Name: "used", Type: kept.typ}, hff.Field{Name: "unused", Type: discarded.typ})
	original := &jsonDeferredValue{typ: typ, present: "source.parent", object: &fields}
	b := builder{ctx: context.Background(), program: p}
	packet, err := b.packDeferredObject(p.Root(), original, map[string]bool{"used": true})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(packet.code, "array_to_json(ARRAY[array_to_json") || strings.Contains(packet.code, "source.present") {
		t.Fatal("unobserved child escaped into payload", packet.code)
	}
	if !strings.Contains(packet.code, "source.kept") || !strings.Contains(packet.code, "'null'::json") || !packet.typ.Equal(typ) {
		t.Fatal("selected payload or static type changed", packet.code)
	}
	decoded, err := packet.decode(&b, p.Root(), "destination.value")
	if err != nil || !decoded.typ.Equal(typ) || decoded.object == nil || len(*decoded.object) != 2 {
		t.Fatal("object shape changed", err)
	}
	out := *decoded.object
	if out[0].name != "used" || out[1].name != "unused" || !out[0].value.leaf.lossy || !out[1].value.typ.Equal(discarded.typ) {
		t.Fatal("field names, order or selected provenance changed")
	}
	if strings.Contains(out[0].value.leaf.code, "source.") || strings.Contains(out[1].value.leaf.code, "source.") {
		t.Fatal("packet decoder captured source scope")
	}
	if original.present != "source.parent" || fields[1].value != discarded || fields[1].value.object == nil {
		t.Fatal("projection mutated its source descriptor")
	}
}

func TestNestedObservedFoldFieldsKeepValidationBounded(t *testing.T) {
	body := "0"
	for depth := 1; depth <= 5; depth++ {
		body = `labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(` + body + `,(a,v)=>a+v.used:string:len)`
		p, _ := foldProjectionProgram(t, body+`>0`)
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
		if ctx.checks > 300+5*nodes*depth {
			t.Fatal("field projection validation replay grew beyond bounded workload", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}
