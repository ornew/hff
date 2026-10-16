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

func TestPostgreSQLNativeNumericFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, nativeNumericFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLNativeNumericFoldExactOutputs(t *testing.T) {
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
	values := `labels:items:map(p=>p[1])`
	numbers := values + `:flatmap(v=>[v:type:len,v:type:len/2])`
	cases := []struct {
		source string
		want   []bool
	}{
		{numbers + `:foldl(0.0,(a,n)=>a+n)=16.5`, []bool{true, false, false, false}},
		{numbers + `:foldr(0.0,(n,a)=>a-n)=-16.5`, []bool{true, false, false, false}},
		{numbers + `:foldl(0.0,(a,n)=>a+n)=15.0`, []bool{false, false, false, true}},
		{numbers + `:foldl("",(a,n)=>a:concat((n:[int]:exists):string))="truefalsetruefalse"`, []bool{true, false, false, true}},
		{numbers + `:foldr("",(n,a)=>a:concat((n:[int]:exists):string))="falsetruefalsetrue"`, []bool{true, false, false, true}},
		{numbers + `:foldl(0.0,(a,n)=>[1]:foldl(a,(b,x)=>b+n))=16.5`, []bool{true, false, false, false}},
		{numbers + `:foldl(0.0,(a,n)=>a+n)=0.0`, []bool{false, true, false, false}},
		{values + `:flatmap(v=>[9007199254740993,9007199254740992.0]):foldl(true,(a,n)=>a (n:[float]:exists | n=9007199254740993))`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>[-9223372036854775808,9223372036854775807,0.0]):foldl("",(a,n)=>a:concat((n:[int]:exists):string))="truetruefalsetruetruefalse"`, []bool{true, false, false, true}},
		{values + `:flatmap(v=>[1,1.0/0]):foldl(true,(a,n)=>a (n=null | n=1))`, []bool{true, true, false, true}},
		{values + `:map(v=>v!):filter(v=>false):flatmap(v=>[1,2.0]):foldl(7.0,(a,n)=>a+n)=7.0`, []bool{true, true, false, false}},
		{values + `:flatmap(v=>[]:concat([1,2.0])):foldl(0.0,(a,n)=>a+n)=6.0`, []bool{true, false, false, true}},
		{values + `:flatmap(v=>[1,2.0]:concat([])):foldr(0.0,(n,a)=>a+n)=6.0`, []bool{true, false, false, true}},
		{values + `:flatmap(v=>[1,-0.0,5e-324]):foldl(true,(a,n)=>a (n:[int]:exists | n=0 | n=5e-324))`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>[0,-0.0]):foldl("",(a,n)=>a:concat(n:string))="0-0.00-0.0"`, []bool{true, false, false, true}},
		{values + `:flatmap(v=>[0,5e-324]):foldl(true,(a,n)=>a (n:[int]:exists | n:string="5e-324"))`, []bool{true, true, false, true}},
		{numbers + `:foldl("",(a,n)=>a:concat(n:type))="numbernumbernumbernumber"`, []bool{true, false, false, true}},
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
	t.Logf("%d independent typed/inferred native numeric outputs", len(expected))
}
