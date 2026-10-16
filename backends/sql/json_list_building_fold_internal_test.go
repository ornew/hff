package sql

import (
	"context"
	"testing"

	"github.com/ornew/hff"
)

func TestListBuildingFoldProofIsLexicalAndDirectional(t *testing.T) {
	for _, test := range []struct {
		fold    string
		want    bool
		reverse bool
	}{
		{`foldl([],(a,v)=>a:concat([v,v:type]))`, true, false},
		{`foldr([],(v,a)=>[v,v:type]:concat(a))`, true, false},
		{`foldl([],(a,v)=>[v,v:type]:concat(a))`, true, true},
		{`foldr([],(v,a)=>a:concat([v,v:type]))`, true, true},
		{`foldl([],(a,v)=>a:concat([[1]:map(a=>a+1),v]))`, true, false},
		{`foldl([],(a,v)=>a:concat([[1]:map(x=>a:size),v]))`, false, false},
		{`foldl([],(a,v)=>a:concat([a,v]))`, false, false},
		{`foldl([],(a,v)=>a:concat(a))`, false, false},
		{`foldl([],(a,v)=>a!:concat([v]))`, false, false},
		{`foldl([],(a,v)=>a:concat([v]):concat([v]))`, false, false},
		{`foldl([0],(a,v)=>a:concat([v]))`, false, false},
		{`foldl(null,(a,v)=>a:concat([v]))`, false, false},
		{`foldl([],(a,v)=>a:concat(v:[list[any]]))`, false, false},
		{`foldl([],(a,v)=>a:concat([]))`, false, false},
	} {
		p, n := foldProjectionProgram(t, `labels:items:map(p=>p[1]):`+test.fold+`:exists`)
		b := builder{ctx: context.Background(), program: p}
		method, _ := n.Method()
		chunk, reverse, err := b.listBuildingFoldProof(n, method.Name())
		if err != nil || chunk.Valid() != test.want || chunk.Valid() && reverse != test.reverse {
			t.Fatal(test.fold, chunk.Valid(), reverse, err)
		}
		b.disableListBuildingFold = true
		if chunk, _, err := b.listBuildingFoldProof(n, method.Name()); err != nil || chunk.Valid() {
			t.Fatal("validation reentered the list-building proof", test.fold, err)
		}
	}
}

func TestListBuildingFoldValidationGrowth(t *testing.T) {
	const values = `labels:items:map(p=>p[1])`
	body := values
	for depth := 1; depth <= 5; depth++ {
		body += `:foldl([],(a,v)=>a:concat([v]))`
		p, _ := foldProjectionProgram(t, body+`:map(v=>v:type):json:exists`)
		ctx := &repeatCheckpointContext{Context: context.Background()}
		q, err := Build(ctx, p, WithDialect(PostgreSQL), WithBindings(BindJSON(hff.Path{"labels"}, Column("records", "labels"), JSON)))
		if err != nil || q == nil {
			t.Fatal(depth, err)
		}
		nodes := listBuildingNodeCount(p.Root())
		if ctx.checks > 1000+12*nodes*depth {
			t.Fatal("list-building validation replay exceeded bounded input", depth, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}

func TestListBuildingFoldNestedChunkValidationGrowth(t *testing.T) {
	// Original 28b5b04 checkpoint counts for this exact fixture. The existing
	// fallback already grows nonlinearly; this ceiling measures additional
	// validation work, not wall time or a polynomial complexity guarantee.
	baseline := []int{234, 661, 1377, 2545, 4559, 8506, 17552, 41100}
	body := "v"
	for i, previous := range baseline {
		depth := i + 1
		body = `labels:items:map(p=>p[1]):foldl([],(a,v)=>a:concat([v,` + body + `]))`
		p, _ := foldProjectionProgram(t, body+`:exists`)
		ctx := &repeatCheckpointContext{Context: context.Background()}
		q, err := Build(ctx, p, WithDialect(PostgreSQL), WithBindings(BindJSON(hff.Path{"labels"}, Column("records", "labels"), JSON)))
		if err != nil || q == nil {
			t.Fatal(depth, err)
		}
		nodes := listBuildingNodeCount(p.Root())
		if ctx.checks > 1000+3*previous {
			t.Fatal("nested chunk validation replay exceeded bounded input", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}

func listBuildingNodeCount(n hff.Node) int {
	count := 1
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		count += listBuildingNodeCount(child)
	}
	return count
}
