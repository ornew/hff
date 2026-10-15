package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type itemRoleFixture struct {
	name, tag           string
	typ                 hff.Type
	first, second, zero hff.Value
	mixed               bool
	metadata            bool
	extraPairs          [][2]hff.Value
}

func itemRoleFixtures() []itemRoleFixture {
	var result []itemRoleFixture
	for _, family := range taggedCollectionCases() {
		if family.container != "list" || family.float || family.name != family.tag+"/list/nestedfalse" {
			continue
		}
		tag := schema.BytesType
		if family.tag == "duration" {
			tag = schema.DurationType
		}
		if family.tag == "timestamp" {
			tag = schema.TimestampType
		}
		for _, mode := range []string{"map", "struct", "mixed"} {
			var typ hff.Type
			switch mode {
			case "map":
				typ = schema.MapOf(schema.NullableType(tag))
			case "struct":
				typ = schema.Struct(schema.Optional(schema.Field("app", schema.NullableType(tag))), schema.Optional(schema.Field("z", schema.NullableType(tag))))
			case "mixed":
				typ = schema.Struct(schema.Optional(schema.String("app")), schema.Optional(schema.Field("z", schema.NullableType(tag))))
			}
			result = append(result, itemRoleFixture{name: family.tag + "/" + mode, tag: family.tag, typ: schema.NullableType(typ), first: family.first, second: family.second, zero: family.zero, mixed: mode == "mixed"})
		}
	}
	result = append(result, itemRoleFixture{name: "native/any", tag: "number", typ: schema.AnyType, first: hff.Int(1), second: hff.Float(1), zero: hff.Int(0), metadata: true})
	return result
}

func itemRoleSchema(tc itemRoleFixture) hff.Type {
	fields := []hff.Field{schema.Optional(schema.Field("labels", tc.typ)), schema.Optional(schema.Field("other", tc.typ))}
	for _, name := range []string{"items_json", "first_json", "keys_json", "filtered_json", "found_json", "values_json"} {
		fields = append(fields, schema.String(name))
	}
	return schema.Struct(fields...)
}

func itemRoleSources(tc itemRoleFixture) []string {
	sources := []string{
		`labels:items:size>0`, `labels:items[0][0]="app"`, `labels:items[-1][-2]:type="string"`,
		`labels:items[0][2]=null`, `labels:items[0][-3]=null`, `labels:items[0][1.5]=null`, `labels:items[0][0.0]:type="string"`,
		`labels:items![0]![0]!:exists`, `labels!:items[0]![0]=null`,
		`labels:items:any(p=>p[0]="app")`, `labels:items:all(p=>p[0]:len>0)`, `labels:items:count(p=>p[0]~"^a")=1`,
		`labels:items:map(p=>p[0])=labels:keys`, `labels:items:map(p=>p[0]:len)[0]=3`,
		`labels:items:map(p=>p[0]):json=keys_json`, `labels:items:map(p=>p[0]):map(key=>key:type):contains("string")`,
		`labels:items:json=items_json`, `labels:items[0]:json=first_json`, `labels:items:unique`,
		`labels:items:distinct:size=labels:size`, `labels:items:distinct=labels:items`,
		`labels:items:filter(p=>p[0]="app"):json=filtered_json`, `labels:items:filter(p=>p[0]="app"):unique`,
		`labels:items:find(p=>p[0]="app"):json=found_json`,
		`labels:items=other:items`, `labels:items!=other:items`, `labels:items[0]=other:items[0]`, `labels:items:contains(other:items[0])`,
		`labels:items:any(p=>other:items:any(q=>p[0]=q[0]))`,
		`labels:items:any(p=>other:items:any(p=>p[0]="app") p[0]="app")`,
	}
	sources = append(sources, itemTransformSources(tc)...)
	if !tc.mixed {
		sources = append(sources, `labels:items[0][1]:type="`+tc.tag+`"`, `labels:items[-1][-1]:type="`+tc.tag+`"`,
			`labels:items:map(p=>p[1]):json=values_json`, `labels:items:any(p=>p[1]!:exists)`, `labels:items:all(p=>p[1]:exists)`, `labels:items:any(p=>p[1])`, `labels:items:filter(p=>p[1]):size>0`,
			`labels:items:map(p=>p[1]:type)[0]="`+tc.tag+`"`, `labels:items:any(p=>other:items:any(q=>q[0]=p[0] q[1]=p[1]) p[1]:exists)`)
	}
	return sources
}

func itemRoleBindings(format hffsql.JSONFormat, metadata ...bool) []hffsql.Binding {
	var bindings []hffsql.Binding
	for _, name := range []string{"labels", "other"} {
		var options []hffsql.JSONBindingOption
		if format == hffsql.JSONB && len(metadata) > 0 && metadata[0] {
			options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
		}
		bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
	}

	for _, name := range []string{"items_json", "first_json", "keys_json", "filtered_json", "found_json", "values_json"} {
		bindings = append(bindings, hffsql.Bind(hff.Path{name}, hffsql.Column("records", name)))
	}
	return bindings
}

func TestJSONItemRoleQueriesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemRoleFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemRoleSources(tc) {
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

func TestJSONItemRolesDoNotBypassCapabilityChecks(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		source string
		denied capabilities.ID
	}{
		{`labels:items:unique`, capabilities.ListEquality},
		{`labels:items:any(p=>p[0]:len>0)`, capabilities.Method("string.len")},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard().Without(test.denied)))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.Compile(ctx, test.source); err == nil {
			t.Error("compiled forbidden operation", test.source)
		}
	}
}

func TestJSONItemRolesDoNotBlessUnprovenPairs(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType), schema.Any("raw"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`labels:items:contains(raw)`, `labels:items[0]=raw`, `raw:json!="[]"`,
		`labels:items:flatmap(p=>[p,raw]):json!:exists`,
		`labels:items:map(p=>raw):json!:exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"raw"}, hffsql.Column("records", "raw"), hffsql.JSONB)))
		if err == nil {
			t.Error("accepted unrecoverable/unproven input", source)
		}
	}
	// Explicit construction retains the two child descriptors. Serialization
	// can now observe them without assigning object-item provenance to an
	// arbitrary list or recovering Bytes from the stored spelling alone.
	p, err := e.Compile(ctx, `labels:items:map(p=>[p[0],p[1]]):json!:exists`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSONB),
		hffsql.BindJSON(hff.Path{"raw"}, hffsql.Column("records", "raw"), hffsql.JSONB))); err != nil {
		t.Fatal(err)
	}
}

// Callbacks operate only on the list alternatives of a union. Object value
// types must not contaminate the stored type of a callback parameter.
func callbackUnionSchema() hff.Type {
	return schema.Struct(schema.Field("data", schema.Union(
		schema.ListOf(schema.NullableType(schema.BytesType)),
		schema.MapOf(schema.StringType), schema.NullType)))
}

func callbackUnionSources() []string {
	return []string{
		`data:any(x=>x:base64="YQ==")`,
		`data:all(x=>x:base64="YQ==")`,
		`data:count(x=>x:base64="YQ==")=1`,
		`data:filter(x=>x:base64="YQ=="):json="[\"YQ==\"]"`,
		`data:map(x=>x:base64):contains("YQ==")`,
		`data:map(x=>x):json="[\"YQ==\"]"`,
		`data:map(x=>x):any(x=>x:base64="YQ==")`,
		`data:flatmap(x=>[x]):json="[\"YQ==\"]"`,
		`data:flatmap(x=>[x]):any(x=>x:base64="YQ==")`,
		`data:find(x=>x:base64="YQ=="):type="bytes"`,
	}
}

func TestJSONCallbacksExcludeNonListUnionBranches(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(callbackUnionSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range callbackUnionSources() {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)))
			if err != nil {
				t.Error(source, format, err)
			}
		}
	}
}

// Identity and duplication retain pair roles, but never imply key uniqueness.
func itemTransformSources(tc itemRoleFixture) []string {
	sources := []string{
		`labels:items:map(p=>p):json=items_json`,
		`labels:items:map(p=>p)=labels:items`,
		`labels:items:map(p=>p):unique`,
		`labels:items:map(p=>p):map(q=>q[0]):json=keys_json`,
		`labels:items:map(p=>p):filter(q=>q[0]="app"):json=filtered_json`,
		`labels:items:map(p=>p):find(q=>q[0]="app"):json=found_json`,
		`labels:items:map(p=>p!):json=items_json`,

		`labels:items:flatmap(p=>[p]):json=items_json`,
		`labels:items:flatmap(p=>[p])=labels:items`,
		`labels:items:flatmap(p=>[p,p]):unique`,
		`labels:items:flatmap(p=>[p,null]):json:contains("null")`,
		`labels:items:flatmap(p=>[p,null]):unique`,
		`labels:items:flatmap(p=>[p,null,null]):distinct:json:contains("null")`,
		`labels:items:flatmap(p=>[p,p]):distinct:json=items_json`,
		`labels:items:flatmap(p=>[p,p]):map(q=>q[0]):size=labels:size*2`,
		`labels:items:flatmap(p=>[p!]):json=items_json`,
		`labels:items:flatmap(p=>other:items):json!:exists`,
		`labels:items:map(p=>other:items:find(q=>q[0]=p[0])):json!:exists`,
		`labels:items:flatmap(p=>other:items:map(q=>p)):json!:exists`,
		`labels:items:flatmap(p=>other:items:map(q=>p)):distinct:json=items_json`,
		`labels:items:map(p=>other:items:find(p=>p[0]="app")):map(q=>q[0]):contains("app")`,
		`labels:items:map(p=>other:items:find(q=>q[0]=p[0])):unique`,
		`[labels:items[0],other:items[0]]:json!:exists`,
		`labels:items:map(p=>p):map(p=>p):json=items_json`,
		`labels:items:flatmap(p=>[p]):flatmap(p=>[p]):json=items_json`,
		`labels:items:map(p=>p):contains(other:items:map(p=>p)[0])`,
	}
	if !tc.mixed {
		sources = append(sources,
			`labels:items:map(p=>p)[0][1]:type="`+tc.tag+`"`,
			`labels:items:flatmap(p=>[p]):any(q=>q[1]:type="`+tc.tag+`")`,
			`labels:items:map(p=>p):map(q=>q[1]):json=values_json`)
	}
	return append(sources, itemTransformGuardSources(tc)...)
}

func itemTransformGuardSources(_ itemRoleFixture) []string {
	return []string{
		`labels!:items:map(p=>p):json!:exists`,
		`labels:items:map(p=>other!:items:find(q=>q[0]=p[0])):json!:exists`,
		`labels:items:flatmap(p=>[p,null]):map(q=>q!):json!:exists`,
		`labels:items:flatmap(p=>[p,null]):flatmap(q=>[q!]):json!:exists`,
	}
}

// Long identity chains exercise repeated negative as well as positive role
// inference. A result without pair origins must remain an ordinary Bytes list.
func TestJSONItemRoleAnalysisHandlesChainedMaps(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType), schema.List("data", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items` + strings.Repeat(`:map(p=>p)`, 12) + `:map(p=>p[0]):contains("app")`,
		`data` + strings.Repeat(`:map(x=>x)`, 12) + `:any(x=>x:base64="YQ==")`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("labels"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
	}
}

func itemFoldSources(tc itemRoleFixture) []string {
	sources := []string{
		`labels:items:foldl(null,(a,p)=>p):json!:exists`,
		`labels:items:foldr(null,(p,a)=>p):json=first_json`,
		`labels:items:foldr(null,(p,a)=>p)[0]="app"`,
		`labels:items:foldl(other:items[0],(a,p)=>a)=other:items[0]`,
		`labels:items:foldl(null,(a,p)=>other:items:find(q=>q[0]=p[0])):json!:exists`,
		`labels:items:foldl(other:items[0],(a,p)=>other:items:foldr(a,(p,a)=>a)):json!:exists`,
		`labels:items:flatmap(p=>[p,null]):foldl(null,(a,p)=>p!):json!:exists`,
		`labels!:items:foldl(null,(a,p)=>p):json!:exists`,
		`labels:items:foldl([], (a,p)=>a:concat([p])):json=items_json`,
		`labels:items:foldr([], (p,a)=>[p]:concat(a)):json=items_json`,
		`labels:items:foldl(other:items,(a,p)=>a):json=other:items:json`,
		`labels:items:foldl([], (a,p)=>[p]):unique`,
		`labels:items:foldl([], (a,p)=>[p,null]):unique`,
		`labels:items:foldl([], (a,p)=>a:concat([p,p])):distinct:json=items_json`,
		`labels:items:foldr([], (p,a)=>[p,p]:concat(a)):distinct:json=items_json`,
		`labels:items:flatmap(p=>[p,null]):foldl([], (a,p)=>a:concat([p!])):json!:exists`,
		`[]:foldl(labels:items,(a,p)=>a):json=items_json`,
		`labels:items:foldl(labels:items,(a,p)=>null):json!:exists`,
	}
	if !tc.mixed {
		sources = append(sources,
			`labels:items:foldl(null,(a,p)=>p)[1]:type="`+tc.tag+`"`,
			`labels:items:foldl([], (a,p)=>a:concat([p])):map(q=>q[1]):json=values_json`)
	}
	return sources
}

func TestJSONItemFoldQueriesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemRoleFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemFoldSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestJSONItemRolesRejectAmbiguousOrigins(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemRoleFixtures() {
		if !strings.HasSuffix(tc.name, "/map") {
			continue
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", tc.typ), schema.Map("right", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{
			`[left:items[0],right:items[0]]:map(p=>p[1]:type):contains("string")`,
			`[left:items[0],right:items[0]]:json!:exists`,
			`left:items:concat(right:items):json!:exists`,
			`left:items:foldl(right:items[0],(a,p)=>p)[1]:type="bytes"`,
			`left:items:foldl(right:items,(a,p)=>[p]):json!:exists`,
			`left:items:foldl([], (a,p)=>a:concat([p,right:items[0]])):json!:exists`,
		} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("rhs_meta")))
				}
				_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("lhs"), format),
					hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("rhs"), format, options...)))
				if err == nil {
					t.Error(tc.name, format, "accepted ambiguous pair origins", source)
				}
			}
		}
	}
}

func TestJSONItemFoldInferenceHandlesChainedSeeds(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	source := `labels:items:foldl(null,(a,p)=>p)`
	for i := 0; i < 12; i++ {
		source = `[]:foldl(` + source + `,(a,p)=>a)`
	}
	p, err := e.Compile(ctx, source+`[1]:type="bytes"`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("labels"), hffsql.JSONB)))
	if err != nil {
		t.Fatal(err)
	}
}
