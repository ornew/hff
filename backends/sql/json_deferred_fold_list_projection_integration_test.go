//go:build integration

package sql_test

import (
	"context"
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

func TestPostgreSQLFixedFoldListsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, buildableFixedFoldListSources, deferredValueFixtures()...)
}

func TestPostgreSQLFixedFoldListExactOutputs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(schema.Optional(schema.Nullable(schema.Bytes("asset"))), schema.Optional(schema.Nullable(schema.String("name"))))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	records := []string{`'{"asset":"YQ==","name":"Alice"}'`, `'{}'`, `NULL`, `'{"asset":null,"name":"Alice"}'`}
	input := `labels:items:map(p=>[p[1],{"unused":[p[1],p[1]]}])`
	cases := []struct {
		source string
		want   []bool
	}{
		{input + `:foldl(0,(a,v)=>a+v[0]:string:len)>5`, []bool{true, false, false, true}},
		{input + `:foldr(0,(v,a)=>a+v[-2]:string:len)>5`, []bool{true, false, false, true}},
		{input + `:foldl("",(a,v)=>a:concat(v[0]:type))="bytesstring"`, []bool{true, false, false, false}},
		{input + `:foldl(0,(a,v)=>a+v[0]!:string:len)>5`, []bool{true, false, false, false}},
		{input + `:foldl(true,(a,v)=>a v[100]=null)`, []bool{true, true, false, true}},
		{input + `:foldl(true,(a,v)=>a v[0.5]=null)`, []bool{true, true, false, true}},
		{`labels:items:map(p=>[p[1],null!]):foldl(0,(a,v)=>a+v[0]:string:len):exists`, []bool{false, true, false, false}},
		{`labels:items:map(p=>[p[1],"middle",p[1]]):foldl("",(a,v)=>a:concat(v[0]:type):concat(v[-1]:type))="bytesbytesstringstring"`, []bool{true, false, false, false}},
		{`labels:items:map(p=>[[],p[1]]):foldl(0,(a,v)=>a+v[0]:size)=0`, []bool{true, true, false, true}},
		{`labels:items:map(p=>[p[1]]:concat([])):foldl(0,(a,v)=>a+v[-1]:string:len)>5`, []bool{true, false, false, true}},
		{`labels:items:map(p=>[[p[1]]:filter(x=>x!=null p[0]:len>0)[0],{"unused":[p[1]]}]):foldl("",(a,v)=>a:concat(v[0]:type))="bytesstring"`, []bool{true, false, false, false}},
	}
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
			requireConcatArgsUsed(t, q)
			for i, record := range records {
				from := `(VALUES (` + record + `::` + string(format) + `)) records(labels)`
				for _, typed := range []bool{false, true} {
					appendNullArgumentQuery(t, &script, q, from, len(expected), typed)
					expected = append(expected, tc.want[i])
					labels = append(labels, tc.source+" "+record)
				}
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("%d independent typed/inferred fixed-list outputs", len(expected))
}
