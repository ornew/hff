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

func TestPostgreSQLNativeChildFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, nativeChildFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLNativeChildFoldExactOutputs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(schema.Optional(schema.Nullable(schema.Bytes("asset"))), schema.Optional(schema.Nullable(schema.String("name"))))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	records := []string{`'{"asset":"YQ==","name":"Alice"}'`, `'{}'`, `NULL`, `'{"asset":null,"name":"Alice"}'`, `'{"asset":"","name":""}'`}
	quoted := func(text string) string {
		b, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	object := `labels:items:map(p=>{"used":p[1]:type:len,"unused":[p[1],p[1]]})`
	list := `labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}])`
	cases := []struct {
		source string
		want   []bool
	}{
		{object + `:foldl(0,(a,v)=>a+v.used)=11`, []bool{true, false, false, false, true}},
		{object + `:foldr(0,(v,a)=>a+v["used"])=10`, []bool{false, false, false, true, false}},
		{object + `:foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v.used))=11`, []bool{true, false, false, false, true}},
		{`labels:items:map(p=>{"a":p[1]:type:len,"b":p[1]:type:len+1,"unused":[p[1]]}):foldl(true,(a,v)=>a v.b=v.a+1)`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"a":false,"b":"","unused":[p[1]]}):foldl(true,(a,v)=>a v.a:exists v.b:exists !v.a !v.b)`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"":p[1]:type,"日本.語":p[1]:type:len,"unused":[p[1]]}):foldl(true,(a,v)=>a v[""]:len=v["日本.語"])`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"used":p[1]:type:duration,"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.used:type))="nullnull"`, []bool{true, false, false, true, true}},
		{`labels:items:map(p=>{"used":p[1]:type:len,"unused":p[1]!}):foldl(0,(a,v)=>a+v.used):exists`, []bool{true, true, false, false, true}},
		{`labels:items:map(p=>{"used":p[1]:type:len,"unused":null!}):foldl(0,(a,v)=>a+v.used):exists`, []bool{false, true, false, false, false}},
		{`labels:items:filter(p=>false):map(p=>{"used":p[1]:type:len,"unused":null!}):foldl(0,(a,v)=>a+v.used)=0`, []bool{true, true, false, true, true}},
		{`labels:items:flatmap(p=>[{"used":p[1]:type:len,"unused":p[1]},{"other":p[1]}]):foldl("",(a,v)=>a:concat(v["used"]:type))="numbernullnumbernull"`, []bool{true, false, false, true, true}},
		{`labels:items:flatmap(p=>[{"used":p[1]:type:len},{"used":p[1]:type:len/2}]):foldl("",(a,v)=>a:concat(v.used:[int]:exists:string))="truefalsetruefalse"`, []bool{true, false, false, true, true}},
		{`labels:items:flatmap(p=>[{"used":p[1]:type:len,"prefix":p[1]},{"prefix":p[1],"used":p[1]:type:len+1}]):foldl(0,(a,v)=>a+v.used)=24`, []bool{true, false, false, false, true}},
		{`labels:items:map(p=>{"i":1,"d":"1ns":duration,"unused":[p[1]]}):foldl(true,(a,v)=>a v.i=1 v.d:nanoseconds=1 v.i:[int]:exists v.d:[duration]:exists)`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"lo":"-2562047h47m16.854775808s":duration,"hi":"2562047h47m16.854775807s":duration,"unused":[p[1]]}):foldl(true,(a,v)=>a v.lo:nanoseconds=-9223372036854775808 v.hi:nanoseconds=9223372036854775807)`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"lo":"0000-01-01T00:00:00.000000001Z":timestamp,"hi":"9999-12-31T23:59:59.999999999Z":timestamp,"unused":[p[1]]}):foldl(true,(a,v)=>a v.lo:string="0000-01-01T00:00:00.000000001Z" v.hi:json="\"9999-12-31T23:59:59.999999999Z\"")`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"a":"":frombase64,"b":` + quoted(allByteBase64()) + `:frombase64,"unused":[p[1]]}):foldl(true,(a,v)=>a v.a:exists v.a:len=0 v.b:base64=` + quoted(allByteBase64()) + ` v.b:json=` + quoted(quoted(allByteBase64())) + `)`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"a":-9223372036854775808,"b":9223372036854775807,"unused":[p[1]]}):foldl(true,(a,v)=>a v.a=-9223372036854775808 v.b=9223372036854775807 v.a:[int]:exists v.b:[int]:exists)`, []bool{true, true, false, true, true}},
		{`labels:items:map(p=>{"a":-0.0,"b":5e-324,"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.a:string):concat(v.b:[float]:exists:string))="-0.0true-0.0true"`, []bool{true, false, false, true, true}},
		{`labels:items:map(p=>{"i":p[1]:type:len,"f":p[1]:type:len/2,"b":p[1]:exists,"s":p[1]:type,"r":"YQ==":frombase64,"d":"1ns":duration,"t":"0000-01-01T00:00:00.000000001Z":timestamp,"unused":[p[1]]}):foldl("",(a,v)=>a:concat(v.i:type):concat(v.f:type):concat(v.b:type):concat(v.s:type):concat(v.r:type):concat(v.d:type):concat(v.t:type))=` + quoted(strings.Repeat("numbernumberboolstringbytesdurationtimestamp", 2)), []bool{true, false, false, true, true}},
		{list + `:foldl(0,(a,v)=>a+v[0]:[int])=11`, []bool{true, false, false, false, true}},
		{list + `:foldr(0,(v,a)=>a+v[-2]:[int])=10`, []bool{false, false, false, true, false}},
		{list + `:foldl(0,(a,v)=>a+v[0]:[int]+v[100]:exists:string:len)=21`, []bool{true, false, false, false, true}},
		{`labels:items:map(p=>[p[1]:type:len,p[1]:type:len+1,{"unused":p[1]}]):foldl(true,(a,v)=>a v[1]:[int]=v[-3]:[int]+1)`, []bool{true, true, false, true, true}},
		{`labels:items:flatmap(p=>[[p[1]:type:len,{"unused":p[1]}],[{"unused":p[1]},p[1]:type:len+1,0.0]]):foldl(0,(a,v)=>a+v[-2]:[int]:exists:string:len)=16`, []bool{true, false, false, true, true}},
		{`labels:items:map(p=>[]:concat([p[1]:type:len,{"unused":p[1]}])):foldl(0,(a,v)=>a+v[0]:[int])=11`, []bool{true, false, false, false, true}},
		{`labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}]:concat([])):foldr(0,(v,a)=>a+v[-2]:[int])=11`, []bool{true, false, false, false, true}},
		{`labels:items:map(p=>[p[1]:type:len,null!]):foldl(0,(a,v)=>a+v[0]:[int]):exists`, []bool{false, true, false, false, false}},
		{object + `:foldl(null!,(a,v)=>v.used):exists`, []bool{false, false, false, false, false}},
		{object + `:foldl(0,(a,v)=>null!):exists`, []bool{false, true, false, false, false}},
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
	t.Logf("%d independent typed/inferred native child outputs", len(expected))
}
