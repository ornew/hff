package sql

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func TestNativeScalarFoldUsesSharedTypedLayout(t *testing.T) {
	order := []hff.TypeKind{hff.IntType, hff.FloatType, hff.BoolType, hff.StringType, hff.BytesType, hff.DurationType, hff.TimestampType}
	sqlTypes := []string{"bigint", "double precision", "boolean", "text", "bytea", "bigint", "numeric"}
	var arms []jsonDeferredArm
	for i := len(order) - 1; i >= 0; i-- {
		kind := order[i]
		// Runtime NULL must not erase this statically proven input family.
		arms = append(arms, jsonDeferredArm{value: jsonParameter{typ: schema.NullableType(hff.PrimitiveType(kind)), lossy: true, direct: &expression{"NULL", "source.present", kind}}})
	}
	arms = append(arms, arms[0], jsonDeferredArm{empty: true, value: jsonParameter{typ: schema.AnyType, code: "unemitted.value", format: JSON}})
	b := builder{ctx: context.Background()}
	layout, err := b.nativeDeferredFoldColumns(jsonDeferredList{arms: arms})
	if err != nil || len(layout) != len(order) {
		t.Fatal(layout, err)
	}
	for i, column := range layout {
		if column.kind != order[i] || column.sql != sqlTypes[i] || column.name == "" {
			t.Fatal("layout changed", layout)
		}
	}
	if layout[0].name == layout[5].name || layout[6].sql != "numeric" {
		t.Fatal("HFF kinds merged by physical type or timestamp narrowed", layout)
	}
	for _, arm := range arms[:len(arms)-1] {
		p, ok := b.nativeScalarDeferredFoldParameter(arm.value)
		if !ok {
			t.Fatal("nullable scalar family rejected", arm.value.typ)
		}
		transport := jsonDeferredFoldTransport{native: &p}
		columns := strings.Split(transport.columns(layout), ",")
		if len(columns) != len(layout) {
			t.Fatal("UNION row width changed", columns)
		}
		for i, column := range layout {
			if !strings.HasSuffix(columns[i], "::"+column.sql+" AS "+column.name) {
				t.Fatal("UNION column family/order changed", columns)
			}
		}
		before := *p.direct
		decoded, err := transport.decode(&b, hff.Node{}, "callback.value")
		if err != nil || decoded.direct.kind != p.direct.kind || !decoded.typ.Equal(p.typ) || decoded.direct.code != "callback.value" || decoded.direct.present != "TRUE" || !decoded.lossy || !reflect.DeepEqual(*p.direct, before) {
			t.Fatal("scalar decoder lost type, scope, null or loss metadata", decoded, err)
		}
	}
	for _, tc := range []struct {
		stored hff.Type
		native hff.TypeKind
	}{
		{schema.DurationType, hff.IntType},
		{schema.IntType, hff.DurationType},
		{schema.TimestampType, hff.IntType},
		{schema.Union(schema.BytesType, schema.StringType), hff.BytesType},
		{schema.Union(schema.DurationType, schema.TimestampType), hff.DurationType},
		{schema.AnyType, hff.StringType},
	} {
		if _, ok := b.nativeScalarDeferredFoldParameter(jsonParameter{typ: tc.stored, direct: &expression{"source.value", "TRUE", tc.native}}); ok {
			t.Fatal("ambiguous or mismatched storage family admitted", tc)
		}
	}
}

func TestNativeScalarFoldValidationIsBounded(t *testing.T) {
	body := `""`
	for depth := 1; depth <= 5; depth++ {
		body = `labels:items:map(p=>p[1]):flatmap(v=>["1ns":duration,v:type,false]):foldl(` + body + `,(a,x)=>a:concat(x:string))`
		p, _ := foldProjectionProgram(t, body+`:exists`)
		ctx := &repeatCheckpointContext{Context: context.Background()}
		q, err := Build(ctx, p, WithDialect(PostgreSQL), WithBindings(BindJSON(hff.Path{"labels"}, Column("records", "labels"), JSON)))
		if err != nil || q == nil {
			t.Fatal(depth, err)
		}
		var count func(hff.Node) int
		count = func(n hff.Node) int {
			result := 1
			for i := 0; i < n.ChildCount(); i++ {
				c, _ := n.Child(i)
				result += count(c)
			}
			return result
		}
		nodes := count(p.Root())
		if ctx.checks > 1000+12*nodes*depth {
			t.Fatal("native scalar validation replay exceeded bounded workload", depth, nodes, ctx.checks)
		}
		t.Logf("depth=%d nodes=%d checkpoints=%d SQLbytes=%d", depth, nodes, ctx.checks, len(q.Where()))
	}
}
