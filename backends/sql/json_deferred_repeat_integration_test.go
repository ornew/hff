//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLRepeatedJSONFlatmapMatchesLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, acceptedRepeatedFlatmapSources, flatmapPipelineFixtures()...)
}

// Expectations are literal values, independent of both backend evaluators.
// Different input elements expose changed multiplicity and reordered parents.
func TestPostgreSQLRepeatedJSONFlatmapExactOutputs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Map("labels", schema.NullableType(schema.BytesType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	values := `labels:items:map(p=>p[1])`
	quote := func(value string) string {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	type testCase struct {
		source, record string
		want           bool
	}
	var cases []testCase
	add := func(source, expected, record string) {
		cases = append(cases, testCase{source + "=" + quote(expected), record, true})
	}
	const input = `'{"app":"YQ==","z":"Yg=="}'`
	for _, tc := range []struct{ body, expected string }{
		{`[v,v]`, `["YQ==","YQ==","Yg==","Yg=="]`},
		{`[v,v,v]`, `["YQ==","YQ==","YQ==","Yg==","Yg==","Yg=="]`},
		{`[v]:concat([v])`, `["YQ==","YQ==","Yg==","Yg=="]`},
		{`[v,v]:concat([v])`, `["YQ==","YQ==","YQ==","Yg==","Yg==","Yg=="]`},
		{`[v]:concat([v]:concat([v,v]))`, `["YQ==","YQ==","YQ==","YQ==","Yg==","Yg==","Yg==","Yg=="]`},
	} {
		add(values+`:flatmap(v=>`+tc.body+`):map(v=>v:base64):json`, tc.expected, input)
	}
	for _, constructor := range []string{
		`[p[1],p[1]]`, `[p[-1],p[-1]]`, `[p[1]!,p[1]!]`, `[p[1]:[bytes],p[1]:[bytes]]`,
		`[p[1]]:concat([p[1]])`, `[[p[1],p[1]][1],[p[1],p[1]][1]]`,
	} {
		add(`labels:items:flatmap(p=>`+constructor+`):map(v=>v:base64):json`, `["YQ==","YQ==","Yg==","Yg=="]`, input)
	}
	add(`labels:items:flatmap(p=>[p[1],p[1],p[1]]):map(v=>v:base64):json`, `["YQ==","YQ==","YQ==","Yg==","Yg==","Yg=="]`, input)
	add(`labels:items:flatmap(p=>[p[0],p[0]]):json`, `["app","app","z","z"]`, input)
	add(`labels:items:flatmap(p=>[p[0]:len+1,p[0]:len+1]):json`, `[4,4,2,2]`, input)
	add(`labels:items:flatmap(p=>[p[1]:base64,p[1]:base64]):json`, `["YQ==","YQ==","Yg==","Yg=="]`, input)
	add(`labels:items:flatmap(p=>[[p[1]],[p[1]]]):flatmap(v=>v):map(v=>v:base64):json`, `["YQ==","YQ==","Yg==","Yg=="]`, input)
	add(`labels:items:flatmap(p=>[[[p[1]]],[[p[1]]]]):flatmap(v=>v):flatmap(v=>v):map(v=>v:base64):json`, `["YQ==","YQ==","Yg==","Yg=="]`, input)
	add(`labels:items:flatmap(p=>[{"payload":p[1]},{"payload":p[1]}]):map(v=>v.payload:base64):json`, `["YQ==","YQ==","Yg==","Yg=="]`, input)
	add(`labels:items:flatmap(p=>[p[0],p[1]]):map(v=>v:string):json`, `["app","YQ==","z","Yg=="]`, input)
	add(`labels:items:flatmap(p=>[p[1],p[0]]):map(v=>v:string):json`, `["YQ==","app","Yg==","z"]`, input)
	for _, constructor := range []string{`[p[1],p[0],p[1]]`, `[p[1]]:concat([p[0],p[1]])`} {
		mixed := `labels:items:flatmap(p=>` + constructor + `)`
		add(mixed+`:map(v=>v:string):json`, `["YQ==","app","YQ==","Yg==","z","Yg=="]`, input)
		add(mixed+`:map(v=>v:type):json`, `["bytes","string","bytes","bytes","string","bytes"]`, input)
		add(mixed+`:foldr("",(v,a)=>v:string:concat(a))`, `YQ==appYQ==Yg==zYg==`, input)
		add(mixed+`:map(v=>v:string):distinct:json`, `["YQ==","app","Yg==","z"]`, input)
		for _, tc := range []struct{ index, expected string }{{"0", "YQ=="}, {"1", "app"}, {"2", "YQ=="}, {"-1", "Yg=="}, {"-2", "z"}, {"-3", "Yg=="}} {
			add(mixed+`[`+tc.index+`]:string`, tc.expected, input)
		}
	}
	add(`labels:items:flatmap(p=>[p[1],[],p[0],p[1],null]):map(v=>v:type):json`, `["bytes","list","string","bytes","null","bytes","list","string","bytes","null"]`, input)
	add(`labels:items:flatmap(p=>[p[1],p[0],p[1],p[0]]):map(v=>v:string):json`, `["YQ==","app","YQ==","app","Yg==","z","Yg==","z"]`, input)
	add(`labels:items:flatmap(p=>[p[0],p[1],p[1.0],p[0]]):map(v=>v:string):json`, `["app","YQ==","YQ==","app","z","Yg==","Yg==","z"]`, input)
	list := values + `:flatmap(v=>[v,v])`
	add(list+`:flatmap(v=>[v,v]):map(v=>v:base64):json`, `["YQ==","YQ==","YQ==","YQ==","Yg==","Yg==","Yg==","Yg=="]`, input)
	add(list+`:concat(`+list+`):map(v=>v:base64):json`, `["YQ==","YQ==","Yg==","Yg==","YQ==","YQ==","Yg==","Yg=="]`, input)
	add(list+`:distinct:map(v=>v:base64):json`, `["YQ==","Yg=="]`, input)
	add(list+`:find(v=>true):base64`, `YQ==`, input)
	add(list+`:foldl("",(acc,v)=>acc:concat(v:base64))`, `YQ==YQ==Yg==Yg==`, input)
	add(list+`:foldr("",(v,acc)=>v:base64:concat(acc))`, `YQ==YQ==Yg==Yg==`, input)
	for _, body := range []string{`[v,v,v]`, `[v]:concat([v,v])`} {
		for _, index := range []string{"0", "1", "2", "-1", "-2", "-3", "3", "-4"} {
			expected := `["YQ==","Yg=="]`
			if index == "3" || index == "-4" {
				expected = `[null,null]`
			}
			add(values+`:flatmap(v=>[`+body+`[`+index+`]]):map(v=>v:base64):json`, expected, input)
		}
	}
	for _, body := range []string{
		`[v:exists,v,v:exists][0]`, `[v:exists,v,v:exists][2]`,
		`[v:exists,v,v:exists][-1]`, `[v:exists,v,v:exists][labels:size-2]`,
		`[v:exists,v,v:exists]:[list[any]][0]`,
		`[v:exists,v,v:exists]:concat([])[0]`,
	} {
		add(values+`:flatmap(v=>[`+body+`]):json`, `[true,true]`, input)
		add(values+`:flatmap(v=>[`+body+`]):json`, `[false,true]`, `'{"app":null,"z":"Yg=="}'`)
	}
	add(list+`:map(v=>v:base64):json`, `[null,null,"Yg==","Yg=="]`, `'{"app":null,"z":"Yg=="}'`)
	add(list+`:map(v=>v:base64):json`, `[]`, `'{}'`)
	add(list+`:map(v=>v:base64):json`, `null`, `NULL`)
	// No-value is not ordinary null, including through its type observer.
	guarded := values + `:map(v=>v!):flatmap(v=>[v,v]):map(v=>v:base64):json`
	cases = append(cases,
		testCase{guarded + `:type="null"`, `'{"app":null,"z":"Yg=="}'`, false},
		testCase{"!" + guarded + `:exists`, `'{"app":null,"z":"Yg=="}'`, true},
		testCase{`!labels:items:flatmap(p=>[p[1]!,p[0],p[1]!]):exists`, `'{"app":null,"z":"Yg=="}'`, true},
		testCase{`!labels:items:flatmap(p=>[p[0],p[1]!,p[0]])[0]:exists`, `'{"app":null,"z":"Yg=="}'`, true},
	)
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, tc := range cases {
			p, err := e.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			from := `(VALUES (` + tc.record + `::` + string(format) + `)) records(labels)`
			appendAggregateProfileQuery(t, &script, q, from, len(expected))
			expected = append(expected, tc.want)
			labels = append(labels, string(format)+" "+tc.source+" "+tc.record)
		}
	}
	floats, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, tc := range []struct{ body, extra, output string }{
			{`[p[1],p[1]]`, "", `["-0.0","-0.0","1.0","1.0"]`},
			{`[p[1]!,p[1]!]`, "", `["-0.0","-0.0","1.0","1.0"]`},
			{`[p[1]:[float],p[1]:[float]]`, "", `["-0.0","-0.0","1.0","1.0"]`},
			{`[p[1],p[0],p[1]]`, "", `["-0.0","\"app\"","-0.0","1.0","\"z\"","1.0"]`},
			{`[p[1],p[1]+0.0,p[1]]`, "", `["-0.0","0.0","-0.0","1.0","1.0","1.0"]`},
			{`[p[1],p[1]+0.0,p[1]]`, `:distinct`, `["-0.0","1.0"]`},
		} {
			source := `labels:items:flatmap(p=>` + tc.body + `)` + tc.extra + `:map(v=>v:json):json=` + quote(tc.output)
			p, err := floats.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			binding := hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, hffsql.WithTypeMetadata(hffsql.Column("records", "labels_meta")))
			if format == hffsql.JSON {
				binding = hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil {
				t.Fatal(err)
			}
			from := `(VALUES ('{"app":-0.0,"z":1.0}'::` + string(format) + `,'{"version":1,"types":{"/app":{"type":"float","text":"-0.0"},"/z":{"type":"float"}}}'::jsonb)) records(labels,labels_meta)`
			appendAggregateProfileQuery(t, &script, q, from, len(expected))
			expected = append(expected, true)
			labels = append(labels, string(format)+" signed-zero "+source)
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
