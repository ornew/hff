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
	add(list+`:map(v=>v:base64):json`, `[null,null,"Yg==","Yg=="]`, `'{"app":null,"z":"Yg=="}'`)
	add(list+`:map(v=>v:base64):json`, `[]`, `'{}'`)
	add(list+`:map(v=>v:base64):json`, `null`, `NULL`)
	// No-value is not ordinary null, including through its type observer.
	guarded := values + `:map(v=>v!):flatmap(v=>[v,v]):map(v=>v:base64):json`
	cases = append(cases,
		testCase{guarded + `:type="null"`, `'{"app":null,"z":"Yg=="}'`, false},
		testCase{"!" + guarded + `:exists`, `'{"app":null,"z":"Yg=="}'`, true},
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
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
