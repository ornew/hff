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

func TestPostgreSQLNativeScalarFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, nativeScalarFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLNativeScalarFoldExactOutputs(t *testing.T) {
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
	records := []string{`'{"asset":"YQ==","name":"Alice"}'`, `'{}'`, `NULL`, `'{"asset":null,"name":"Alice"}'`}
	quoted := func(text string) string {
		raw, err := json.Marshal(text)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	values := `labels:items:map(p=>p[1])`
	textBool := values + `:flatmap(v=>[v:type,v:type:len>4])`
	allBytes := quoted(allByteBase64()) + `:frombase64`
	bytesInt := values + `:flatmap(v=>[` + allBytes + `,v:type:len])`
	durationBounds := values + `:flatmap(v=>["-2562047h47m16.854775808s":duration,"2562047h47m16.854775807s":duration,1])`
	durationParts := values + `:flatmap(v=>["1ns":duration,"-1ns":duration,"1.000000001s":duration,1])`
	timestampBounds := values + `:flatmap(v=>["0000-01-01T00:00:00.000000001Z":timestamp,"9999-12-31T23:59:59.999999999Z":timestamp,1])`
	timestampEpoch := values + `:flatmap(v=>["1969-12-31T23:59:59.999999999Z":timestamp,"1970-01-01T00:00:00.000000001Z":timestamp,1])`
	cases := []struct {
		source string
		want   []bool
	}{
		{textBool + `:foldl("",(a,x)=>a:concat(x:string))="bytestruestringtrue"`, []bool{true, false, false, false}},
		{textBool + `:foldl("",(a,x)=>a:concat(x:string))="nullfalsestringtrue"`, []bool{false, false, false, true}},
		{textBool + `:foldr("",(x,a)=>a:concat(x:string))="truestringtruebytes"`, []bool{true, false, false, false}},
		{values + `:flatmap(v=>[v:type:len,1.0,false,v:type,"YQ==":frombase64,"1ns":duration,"0000-01-01T00:00:00.000000001Z":timestamp]):foldl("",(a,x)=>a:concat(x:type))=` + quoted(strings.Repeat("numbernumberboolstringbytesdurationtimestamp", 2)), []bool{true, false, false, true}},
		{values + `:flatmap(v=>[false,""]):foldl(true,(a,x)=>a x:exists)`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>[false,""]):foldl(true,(a,x)=>a !x)`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>["A\nあ😀",false]):foldl("",(a,x)=>a:concat(x:string))=` + quoted("A\nあ😀falseA\nあ😀false"), []bool{true, false, false, true}},
		{values + `:flatmap(v=>["":frombase64,false]):foldl(true,(a,x)=>a x:exists)`, []bool{true, true, false, true}},
		{bytesInt + `:foldl(true,(a,x)=>a (x:[int]:exists | x:[bytes]:base64=` + quoted(allByteBase64()) + `))`, []bool{true, true, false, true}},
		{bytesInt + `:foldl(true,(a,x)=>a (x:[int]:exists | x:[bytes]:json=` + quoted(quoted(allByteBase64())) + `))`, []bool{true, true, false, true}},
		{bytesInt + `:foldl("",(a,x)=>a:concat(x:string))=` + quoted(allByteBase64()+"5"+allByteBase64()+"6"), []bool{true, false, false, false}},
		{durationBounds + `:foldl(true,(a,x)=>a (x:[int]:exists | x:[duration]:nanoseconds=-9223372036854775808 | x:[duration]:nanoseconds=9223372036854775807))`, []bool{true, true, false, true}},
		{durationBounds + `:foldl("",(a,x)=>a:concat(x:string))=` + quoted(strings.Repeat("-2562047h47m16.854775808s2562047h47m16.854775807s1", 2)), []bool{true, false, false, true}},
		{durationParts + `:foldl("",(a,x)=>a:concat(x:string))=` + quoted(strings.Repeat("0.000000001s-0.000000001s1.000000001s1", 2)), []bool{true, false, false, true}},
		{durationParts + `:foldl("",(a,x)=>a:concat(x:json))=` + quoted(strings.Repeat(`"0.000000001s""-0.000000001s""1.000000001s"1`, 2)), []bool{true, false, false, true}},
		{timestampBounds + `:foldl(true,(a,x)=>a (x:[int]:exists | x:[timestamp]:string="0000-01-01T00:00:00.000000001Z" | x:[timestamp]:string="9999-12-31T23:59:59.999999999Z"))`, []bool{true, true, false, true}},
		{timestampBounds + `:foldl("",(a,x)=>a:concat(x:json))=` + quoted(strings.Repeat(`"0000-01-01T00:00:00.000000001Z""9999-12-31T23:59:59.999999999Z"1`, 2)), []bool{true, false, false, true}},
		{timestampEpoch + `:foldl("",(a,x)=>a:concat(x:string))=` + quoted(strings.Repeat("1969-12-31T23:59:59.999999999Z1970-01-01T00:00:00.000000001Z1", 2)), []bool{true, false, false, true}},
		{values + `:flatmap(v=>["1969-12-31T23:59:59.999999001Z":timestamp,"1970-01-01T00:00:00.000000999Z":timestamp,1]):foldl(true,(a,x)=>a (x:[int]:exists | x:[timestamp]:string="1969-12-31T23:59:59.999999001Z" | x:[timestamp]:string="1970-01-01T00:00:00.000000999Z"))`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>[v:type:duration,1]):foldl("",(a,x)=>a:concat(x:type))="nullnumbernullnumber"`, []bool{true, false, false, true}},
		{values + `:flatmap(v=>["9999-12-31T23:59:59.999999999Z":timestamp:add("1ns":duration),1]):foldl("",(a,x)=>a:concat(x:type))="nullnumbernullnumber"`, []bool{true, false, false, true}},
		{textBool + `:foldl("",(a,x)=>[1]:foldl(a,(b,n)=>b:concat(x:string)))="bytestruestringtrue"`, []bool{true, false, false, false}},
		{values + `:map(v=>v!):filter(v=>false):flatmap(v=>["1ns":duration,false]):foldl("seed",(a,x)=>a:concat(x:string))="seed"`, []bool{true, true, false, false}},
		{values + `:flatmap(v=>[]:concat(["1ns":duration,false])):foldl(true,(a,x)=>a x:exists)`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>["1ns":duration,false]:concat([])):foldr(true,(x,a)=>a x:exists)`, []bool{true, true, false, true}},
		{values + `:flatmap(v=>["1ns":duration,false]):foldl(null!,(a,x)=>x:string):exists`, []bool{false, false, false, false}},
		{values + `:flatmap(v=>["1ns":duration,false]):foldl("",(a,x)=>null!):exists`, []bool{false, true, false, false}},
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
	t.Logf("%d independent typed/inferred native scalar outputs", len(expected))
}
