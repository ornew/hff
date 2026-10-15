package sql_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func itemSlotSources(tc itemRoleFixture) []string {
	indexes := itemSlotIndexes
	sources := []string{
		`labels:items[0][1]:type="` + tc.tag + `"`,
		`labels:items[-1][-1]:type="` + tc.tag + `"`,
		`labels:items[0][0]:[any]:type="string"`,
		`labels:items[0][2]:[any]:type="null"`,
		`labels:items[0][1]!:exists`,
		`labels:items[0][1]!:type="null"`,
		`labels:items[0][1]:[string]:len>0`,
		`labels:items[0][1]:[int]>0`,
		`labels:items[0][1]:[float]>0`,
		`labels:items[0][1]:[number]>0`,
		indexes + `:count(i=>labels:items[0][i]:type="string")=2`,
		indexes + `:count(i=>labels:items[0][i]:type="` + tc.tag + `")=3`,
		indexes + `:any(i=>labels:items[0][i]:exists)`,
		indexes + `:count(i=>labels:items[0][i]:[null]=null)>0`,
		indexes + `:any(i=>labels:items[0][i]:[string]:len>0)`,
		indexes + `:any(i=>labels:items[0][i!]:type="null")`,
		indexes + `:any(i=>labels:items[0][i:[int]]:type="string")`,
		indexes + `:any(i=>labels:items[0][i]!:type="string")`,
		indexes + `:any(i=>labels!:items[0]![i]:[null]=null)`,
		`labels:items:[null][0][labels!:size]:type="null"`,
		`labels:items:any(p=>other:items:any(q=>p[1]:type=q[1]:type))`,
		`labels:items:any(p=>other:items:any(p=>p[1]:[string]:len>0) p[1]:[string]:len>0)`,
		`labels:items:map(p=>p[1]:[string]:len):size=labels:size`,
		`labels:items[0]:[list[string|null]][1]:type="null"`,
	}
	if tc.typ.Kind() != hff.AnyType {
		sources = append(sources, itemSlotTaggedGuardSources()...)
	}
	return sources
}

const itemSlotIndexes = `[0,1,-1,-2,2,-3,1.5,1.0,-1.0,null,"x",true,false,2147483648,-2147483649,1e30,-1e30]`

func itemSlotFixtures() []itemRoleFixture {
	fixtures := itemRoleFixtures()
	fixtures = append(fixtures,
		itemRoleFixture{name: "nested/map", tag: "list", typ: schema.NullableType(schema.MapOf(schema.NullableType(schema.Union(schema.ListOf(schema.BytesType), schema.MapOf(schema.BytesType))))), first: hff.List(hff.Bytes([]byte("a"))), second: hff.Object(map[string]hff.Value{"inner": hff.Bytes([]byte("b"))}), zero: hff.List()},
		itemRoleFixture{name: "numbers/map", tag: "number", typ: schema.NullableType(schema.MapOf(schema.NullableType(schema.Union(schema.IntType, schema.FloatType)))), first: hff.Int(1), second: hff.Float(1), zero: hff.Float(0), metadata: true},
	)
	return fixtures
}

func itemSlotTaggedGuardSources() []string {
	indexes := itemSlotIndexes
	return []string{
		`{"app":"auth","z":labels:items[0][1]:[bytes|null]}:items[0][1]:type="string"`,
		`labels:items[0][1]:[bytes]:len>0`,
		`labels:items[0][1]:[duration]:nanoseconds>0`,
		`labels:items[0][1]:[timestamp]:year>=0`,
		indexes + `:any(i=>labels:items[0][i]:[bytes]:len>0)`,
		indexes + `:any(i=>labels:items[0][i]:[duration]:nanoseconds>0)`,
		indexes + `:any(i=>labels:items[0][i]:[timestamp]:year>=0)`,
	}
}

func TestJSONItemSlotObserversBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemSlotFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemSlotSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
		if tc.typ.Kind() == hff.AnyType {
			// Native JSON Any does not provide tagged scalar identity. Keep
			// these rejection cases instead of guessing a tag from a string.
			for _, source := range itemSlotTaggedGuardSources() {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
					_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, true)...))
					if err == nil {
						t.Error("native Any invented tagged identity", format, source)
					}
				}
			}
		}
	}
}

func TestJSONItemSlotGuardsRejectErasedTagIdentity(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Field("app", schema.AnyType), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items[0][1]:[any]:type="string"`,
		`labels:items[0][1]:[string|bytes]:type="string"`,
		`labels:items[0][labels:size-1]:[any]:exists`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, true)...))
			if err == nil {
				t.Error("guard erased selected String/Bytes identity", format, source)
			}
		}
	}
}
