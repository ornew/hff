package sql_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func itemKeyCases() []struct {
	source string
	keys   bool
} {
	return []struct {
		source string
		keys   bool
	}{
		{`labels:items:any(p=>p[0]="app")`, true},
		{`labels:items:all(p=>p[0]:len>0)`, true},
		{`labels:items:count(p=>p[0]:startswith("a"))=1`, true},
		{`labels:items:any(p=>p[-2]="app")`, true},
		{`labels:items:any(p=>p![0.0]="app")`, true},
		{`labels!:items!:any(p=>p[0]="app")`, true},
		{`!labels:items!:any(p=>p[0]="app")`, true},
		{`labels:items:map(p=>p[0])=labels:keys`, true},
		{`labels:items:map(p=>p[0]):json=keys_json`, true},
		{`labels:items:map(p=>p[0]:[string]):json=keys_json`, true},
		{`labels:items:flatmap(p=>[p[0]]):json=keys_json`, true},
		{`labels:items:map(p=>p[0]:number!):exists`, true},
		{`labels:items:flatmap(p=>p[0]:fromjson):exists`, true},
		{`labels:items:sum(p=>p[0]:len)>=0`, true},
		{`labels:items:avg(p=>p[0]:len)>0`, true},
		{`labels:items:min(p=>p[0]:len)>=0`, true},
		{`labels:items:max(p=>p[0]:len)>=0`, true},
		{`labels:items:sum(p=>1)=labels:size`, true},
		{`labels:items:any(p=>other:items:any(q=>q[0]=p[0]))`, true},
		{`labels:items:any(p=>other:items:any(p=>p[0]="app") p[0]="app")`, true},
		{`labels:items:map(p=>other:items:map(q=>p[0])):size=labels:size`, true},
		{`labels:items:count=labels:size`, true},
		{`labels:items:size=labels:size`, true},
		{`labels:items:any(p=>p[1]:exists)`, false},
		{`labels:items:map(p=>p):json=items_json`, false},
		{`labels:items:filter(p=>p[0]="app"):json=filtered_json`, false},
		{`labels:items:find(p=>p[0]="app"):json=found_json`, false},
	}
}

func itemKeySources(fixture itemRoleFixture) []string {
	var sources []string
	for _, test := range itemKeyCases() {
		if fixture.mixed && test.source == `labels:items:any(p=>p[1]:exists)` {
			continue
		}
		sources = append(sources, test.source)
	}
	sources = append(sources,
		`{"app": labels!}:items:any(p=>p[0]="app")`,
		`labels:items:any(p=>other!:items:size>0 p[0]="app")`,
	)
	return sources
}

func itemKeyOnlySources(_ itemRoleFixture) []string {
	var sources []string
	for _, test := range itemKeyCases() {
		if test.keys {
			sources = append(sources, test.source)
		}
	}
	return sources
}

func TestJSONItemKeysAvoidValueAndPairConstruction(t *testing.T) {
	ctx := context.Background()
	fixture := itemRoleFixtures()[0]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(fixture)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	pairConstruction := regexp.MustCompile(`json_build_array\([^,()]+\.key,[^,()]+\.value\)`)
	for _, test := range itemKeyCases() {
		p, err := e.Compile(ctx, test.source)
		if err != nil {
			t.Fatal(test.source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format)...))
			if err != nil {
				t.Fatal(test.source, format, err)
			}
			pairs := pairConstruction.MatchString(q.Where())
			if pairs == test.keys {
				t.Errorf("%s %s: pair construction=%t, key-only=%t", test.source, format, pairs, test.keys)
			}
			if test.keys && !strings.Contains(q.Where(), string(format)+"_object_keys(") {
				t.Error("key-only consumer did not directly traverse object keys", test.source, format)
			}
			if test.source == `labels:items:any(p=>p[0]="app")` || test.source == `labels:items:map(p=>p[0]):json=keys_json` {
				if strings.Contains(q.Where(), "decode(") || strings.Contains(q.Where(), "json_object_agg(") {
					t.Error("key-only consumer restored unused object values", test.source, format)
				}
				t.Logf("%s %s: %d WHERE bytes", test.source, format, len(q.Where()))
			}
		}
	}
}

func TestJSONItemKeysKeepContentObservationsGeneric(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`labels:items:any(p=>p:[list[string]][0]="app")`,
		`labels:items:any(p=>p[1-1]="app")`,
		`labels:items:map(p=>p:[list[string]][0]):json!:exists`,
		`labels:items:[list[list[string]]]:any(p=>p[0]="app")`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if err != nil {
				t.Fatal(source, format, err)
			}
			if strings.Contains(q.Where(), string(format)+"_object_keys(") {
				t.Error("content-dependent guard or dynamic index discarded", source, format)
			}
		}
	}
}

func keyOnlyValueFixtures() []itemRoleFixture {
	return []itemRoleFixture{
		{name: "native/any/no-metadata", typ: schema.AnyType, first: hff.Int(1), second: hff.Float(1), zero: hff.Int(0)},
		{name: "mixed/numbers/no-metadata", typ: schema.NullableType(schema.MapOf(schema.NullableType(schema.Union(schema.IntType, schema.FloatType)))), first: hff.Int(1), second: hff.Float(1), zero: hff.Int(0)},
		{name: "mixed/string-bytes/no-metadata", typ: schema.NullableType(schema.MapOf(schema.NullableType(schema.Union(schema.StringType, schema.BytesType)))), first: hff.String("YQ=="), second: hff.Bytes([]byte("a")), zero: hff.Bytes(nil)},
	}
}

func TestJSONItemKeysDoNotRequireUnobservedValueIdentity(t *testing.T) {
	ctx := context.Background()
	for _, fixture := range keyOnlyValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(fixture)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemKeyOnlySources(fixture) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSONB)...))
			if err != nil {
				t.Error(fixture.name, source, err)
			}
		}
	}
}

func TestJSONItemKeysKeepOtherObservedValueChecks(t *testing.T) {
	ctx := context.Background()
	for _, fixture := range keyOnlyValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(fixture)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		// The item binding itself is unused, but a different observed value
		// still requires subtype restoration before it can be serialized.
		p, err := e.Compile(ctx, `labels:items:map(p=>other):json!:exists`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSONB)...))
		if err == nil {
			t.Error("key-only source bypassed a captured value's recovery", fixture.name)
		}
	}
}
