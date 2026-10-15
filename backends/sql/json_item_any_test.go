package sql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func itemAnyFixtures() []itemRoleFixture {
	var fixtures []itemRoleFixture
	for _, tc := range itemRoleFixtures() {
		if !tc.mixed {
			continue
		}
		tag := map[string]hff.Type{"bytes": schema.BytesType, "duration": schema.DurationType, "timestamp": schema.TimestampType}[tc.tag]
		tc.name = tc.tag + "/any-field"
		tc.typ = schema.NullableType(schema.Struct(schema.Optional(schema.Field("app", schema.AnyType)), schema.Optional(schema.Field("z", schema.NullableType(tag)))))
		tc.metadata = true
		for _, value := range []hff.Value{
			hff.String("YQ=="), hff.String("1ns"), hff.String("2026-01-01T00:00:00Z"),
			hff.Int(1), hff.Float(1), hff.Bool(false), hff.Bool(true),
			hff.List(), hff.List(hff.Int(1), hff.Float(1), hff.String("YQ==")),
			hff.Object(map[string]hff.Value{"n": hff.Int(1)}), hff.Object(nil),
		} {
			tc.extraPairs = append(tc.extraPairs, [2]hff.Value{
				hff.Object(map[string]hff.Value{"app": value, "z": tc.first}),
				hff.Object(map[string]hff.Value{"app": value, "z": tc.second}),
			})
		}
		fixtures = append(fixtures, tc)
		tc.name += "/nullable-any"
		tc.typ = schema.NullableType(schema.Struct(schema.Optional(schema.Field("app", schema.NullableType(schema.AnyType))), schema.Optional(schema.Field("z", schema.NullableType(tag)))))
		fixtures = append(fixtures, tc)
	}
	return fixtures
}

func itemAnySources(tc itemRoleFixture) []string {
	var sources []string
	for _, slot := range []string{
		`labels:items[0][1]`,
		`labels:items[-1][-1]`,
		`labels:items[0][labels:size-1]`,
	} {
		for _, tag := range []string{"string", "number", "bool", "list", "object", "null", tc.tag} {
			sources = append(sources, slot+`:type="`+tag+`"`, slot+`:[any]:[any]:type="`+tag+`"`)
		}
		sources = append(sources,
			slot+`:[any]:exists`, slot+`:[any]!:exists`,
			slot+`:[any]:[string]:len>0`,
			slot+`:[any]:[int]>0`, slot+`:[any]:[float]>0`,
			slot+`:[null]:[any]:type="null"`,
			slot+`:[string]:[null]=null`,
			slot+`:[null]!:[any]:exists`,
		)
	}
	return append(sources,
		`labels:items:any(p=>p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:map(p=>p[1]:[string]:len):size=labels:size`,
		`labels:items:filter(p=>p[1]:[any]:type="`+tc.tag+`"):size=1`,
		`labels:items:[list[list[any]]]:any(p=>p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:map(p=>p:[list[any]]):any(p=>p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:flatmap(p=>[p]):any(p=>p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:foldl([], (acc,p)=>acc:concat([p])):any(p=>p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:foldr([], (p,acc)=>[p]:concat(acc)):any(p=>p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:find(p=>p[0]="z")[1]:[any]:type="`+tc.tag+`"`,
		`labels:items:any(p=>other:items:any(p=>p[1]:[any]:type="string") p[1]:[any]:type="`+tc.tag+`")`,
		`labels:items:[list[list[string|null]]]:concat(other:items:[list[list[string|null]]]):any(p=>p[1]:type="string")`,
		`labels:items:[list[list[string|null]]]:foldl([], (acc,p)=>acc:concat([p])):any(p=>p[1]:[any]:type="string")`,
		`labels:items:[list[list[string|null]]]:foldr([], (p,acc)=>[p]:concat(acc)):any(p=>p[1]:[any]:type="string")`,
	)
}

func TestJSONItemAnyFieldObserversBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemAnyFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemAnySources(tc) {
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
	}
}

func TestJSONItemAnyObserversRejectUnrecoverableOrigins(t *testing.T) {
	ctx := context.Background()
	for _, typ := range []hff.Type{
		schema.Union(schema.AnyType, schema.BytesType),
		schema.Union(schema.StringType, schema.BytesType),
		schema.ListOf(schema.Union(schema.AnyType, schema.BytesType)),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Field("app", typ), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{
			`labels:items[0][1]:type="string"`,
			`labels:items[0][1]:[any]:type="string"`,
			`labels:items[0][1]:[string]:len>0`,
			`labels:items:map(p=>p[1]:[any]:type):size>0`,
		} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemSlotSourceBinding(format)))
				var diagnostic *hff.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != hff.BackendError {
					t.Error(typ.Key(), format, source, "want representation rejection, got", err)
				}
			}
		}
	}
}
