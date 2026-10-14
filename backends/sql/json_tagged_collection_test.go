package sql_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type taggedCollectionCase struct {
	name, tag, container string
	typ                  hff.Type
	first, second, zero  hff.Value
	collection, empty    hff.Value
	withNull             hff.Value
	float                bool
}

func taggedCollectionCases() []taggedCollectionCase {
	var cases []taggedCollectionCase
	for _, family := range []struct {
		name                string
		typ                 hff.Type
		first, second, zero hff.Value
	}{
		{"bytes", schema.BytesType, hff.Bytes([]byte("a")), hff.Bytes([]byte("b")), hff.Bytes(nil)},
		{"duration", schema.DurationType, hff.Duration(time.Nanosecond), hff.Duration(2 * time.Nanosecond), hff.Duration(0)},
		{"timestamp", schema.TimestampType, hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 123, time.UTC)), hff.Timestamp(time.Date(2027, 1, 1, 0, 0, 0, 125, time.UTC)), hff.Timestamp(time.Unix(0, 0))},
	} {
		for _, nested := range []bool{false, true} {
			element, first := schema.IntType, hff.Int(1)
			if nested {
				element, first = family.typ, family.first
			}
			element = schema.NullableType(element)
			for _, container := range []string{"list", "map", "struct"} {
				var typ hff.Type
				var value, empty, withNull hff.Value
				switch container {
				case "list":
					typ = schema.ListOf(element)
					value, empty, withNull = hff.List(first), hff.List(), hff.List(hff.Null())
				case "map":
					typ = schema.MapOf(element)
					value, empty, withNull = hff.Object(map[string]hff.Value{"n": first}), hff.Object(nil), hff.Object(map[string]hff.Value{"n": hff.Null()})
				case "struct":
					typ = schema.Struct(schema.Optional(schema.Field("n", element)))
					value, empty, withNull = hff.Object(map[string]hff.Value{"n": first}), hff.Object(nil), hff.Object(map[string]hff.Value{"n": hff.Null()})
				}
				cases = append(cases, taggedCollectionCase{
					name: fmt.Sprintf("%s/%s/nested%t", family.name, container, nested), tag: family.name, container: container,
					typ:   schema.Union(family.typ, typ, schema.IntType, schema.NullType),
					first: family.first, second: family.second, zero: family.zero, collection: value, empty: empty, withNull: withNull,
				})
			}
		}
		cases = append(cases, taggedCollectionCase{
			name: family.name + "/mixed-numbers", tag: family.name, container: "list", float: true,
			typ:   schema.Union(family.typ, schema.ListOf(schema.Union(schema.IntType, schema.FloatType, schema.NullType)), schema.IntType, schema.FloatType, schema.NullType),
			first: family.first, second: family.second, zero: family.zero,
			collection: hff.List(hff.Int(1), hff.Float(1)), empty: hff.List(), withNull: hff.List(hff.Null()),
		})
	}
	return cases
}

func taggedCollectionSchema(tc taggedCollectionCase) hff.Type {
	return schema.Struct(schema.Optional(schema.Field("value", tc.typ)), schema.Optional(schema.Field("other", tc.typ)),
		schema.Nullable(schema.List("items", tc.typ)), schema.String("serialized"), schema.String("items_json"))
}

func taggedCollectionSources(tc taggedCollectionCase) []string {
	sources := []string{
		`value`, `!value`, `value:type="` + tc.tag + `"`, `value:[` + tc.tag + `]:exists`,
		`value=other`, `other=value`, `value!=other`, `(value=other):type="null"`,
		`value<other`, `other>=value`, `(value<other):type="null"`,
		`value:in(other)`, `other:in(value)`, `value!:in(other!)=null`,
		`value:json=serialized`, `value:size>0`,
		`("YQ==":contains(value)):type="null"`, `("YQ==":contains(value,other!:json)):type="null"`,
		`items:contains(value)`, `[value]:contains(other)`, `!([value!]:contains(other))`,
		`items:unique`, `items:distinct:size=1`, `items:distinct:contains(value)`, `items:sort:exists`,
		`items:any(x=>x:[` + tc.tag + `]:exists)`, `items:all(x=>x:exists)`,
		`items:filter(x=>x):count=1`, `items:find(x=>x:exists)=value`,
		`items:map(x=>x):json=items_json`, `items:flatmap(x=>[x])=items`,
		`items:foldl(0,(acc,x)=>x)=other`, `items:foldr(0,(x,acc)=>x)=value`,
		`items:foldl(0,(acc,x)=>x!):exists`, `items:map(x=>[x!]):flatmap(x=>x)=items`,
	}
	if tc.container == "list" {
		sources = append(sources, `value[0]:exists`, `value[other]=null`, `value:[list[number]]:size>0`, `value:map(x=>x):size>0`, `value:filter(x=>x):size>0`, `value:contains(other)`, `value:unique`, `value:concat([0]):size>0`)
	} else {
		sources = append(sources, `value.n=null`, `value!.n=null`, `value:get("n"):exists`, `value:get(other)=null`, `value:has(other):type="null"`, `value:values:map(x=>x:type):size>0`, `value:items:size>0`, `value:keys:size>0`)
	}
	return sources
}

func TestJSONTaggedCollectionUnionsBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range taggedCollectionCases() {
		e, err := engine.New(engine.WithSchema(taggedCollectionSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range taggedCollectionSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var bindings []hffsql.Binding
				for _, name := range []string{"value", "other", "items"} {
					var options []hffsql.JSONBindingOption
					if format == hffsql.JSONB && tc.float {
						options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
					}
					bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
				}
				bindings = append(bindings, hffsql.Bind(hff.Path{"serialized"}, hffsql.Column("records", "serialized")), hffsql.Bind(hff.Path{"items_json"}, hffsql.Column("records", "items_json")))
				if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestJSONTaggedCollectionAmbiguitiesRemainRejected(t *testing.T) {
	ctx := context.Background()
	for _, tag := range []hff.Type{schema.BytesType, schema.DurationType, schema.TimestampType} {
		for _, typ := range []hff.Type{
			schema.Union(tag, schema.StringType, schema.ListOf(schema.IntType)),
			schema.Union(schema.ListOf(tag), schema.ListOf(schema.StringType)),
			schema.ListOf(schema.Union(tag, schema.StringType)),
		} {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("value", typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			p, err := e.Compile(ctx, `value=value`)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), format, options...)))
				if err == nil {
					t.Errorf("accepted ambiguous tagged schema %s in %s", typ.Key(), format)
				}
			}
		}
	}
}

func TestJSONObjectDynamicIndexDoesNotRequireNumericMetadata(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("data", schema.IntType), schema.Field("key", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data[key]=1`, `data[key]:type="null"`, `data[key!]:exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"key"}, hffsql.Column("records", "key"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
	}
}
