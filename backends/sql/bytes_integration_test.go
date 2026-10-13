//go:build integration

package sql_test

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLBytesParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	all := make([]byte, 256)
	for i := range all {
		all[i] = byte(i)
	}
	encoded := base64.StdEncoding.EncodeToString(all)
	rawEncoded := base64.RawStdEncoding.EncodeToString(all)
	var script strings.Builder
	var wants []bool
	var labels []string
	type fixture struct {
		typ     hff.Type
		storage string
		values  []hff.Value
		sources []string
	}
	texts := []hff.Value{hff.Null(), hff.String(encoded), hff.String(rawEncoded)}
	for _, s := range []string{"", "AA==", "AAA=", "AAAA", "AB==", "AAB=", "A===", "AA", "AAAA=", "AA==\n", "AA==\r", "AA== ", "-w==", "_w==", "é===", "====", "AAAA\nAAAA"} {
		texts = append(texts, hff.String(s))
	}
	for _, s := range []string{"Zg", "Zm8", "Zh", "Zm9", "Z", "AAEC/w", "Zg=", "Zg\n", "Z\rg", " Zg", "Zg ", "Zg\t", "_w", "-w", "é"} {
		texts = append(texts, hff.String(s))
	}
	fixtures := []fixture{
		{schema.Union(schema.BytesType, schema.NullType), "bytea", []hff.Value{hff.Null(), hff.Bytes(nil), hff.Bytes([]byte{0}), hff.Bytes(all)}, []string{`data`, `!data`, `data:type="bytes"`, `data:len=256`, `data:base64:string=data:string`, `data:base64:frombase64=data`, `data:base64r:frombase64r=data`, `data:base64r:frombase64r:len=data:len`, `data:base64r=""`, `data:json=data:base64:json`, `data!:base64:exists`, `data!:json="null"`, `data:base64=""`, `data:base64:frombase64=null`, `data:exists=([null:frombase64]:json="[null]")`, `data:exists=[(null!):frombase64]:exists`}},
		{schema.Union(schema.StringType, schema.NullType), "text", texts, []string{`data:frombase64`, `data:frombase64r`, `data:frombase64=null`, `data:frombase64r=null`, `data:frombase64:base64=data`, `data:frombase64r:base64r=data`, `data:frombase64:type="bytes"`, `data:frombase64r:type="bytes"`, `data:frombase64:len=256`, `data:frombase64r:len=256`, `data:frombase64:string=""`, `data:frombase64:json="null"`, `data!:frombase64:exists`, `data:frombase64=("AB==":frombase64)`, `data:frombase64r=("Zh":frombase64r)`, `data:frombase64=("AA==\n":frombase64)`}},
		{schema.AnyType, "json", append(append([]hff.Value{}, texts...), hff.Int(1), hff.Bool(false), hff.List(), hff.Object(nil)), []string{`data:frombase64=null`, `data:frombase64:base64=data`, `data:frombase64:type="bytes"`, `data!:frombase64:exists`, `data:frombase64:json="null"`}},
		{schema.AnyType, "jsonb", texts, []string{`data:frombase64:base64=data`, `data:frombase64=null`}},
	}

	for _, storage := range []string{"json", "jsonb"} {
		fixtures = append(fixtures,
			fixture{schema.MapOf(schema.Union(schema.BytesType, schema.NullType)), storage, []hff.Value{hff.Object(nil), hff.Object(map[string]hff.Value{"a": hff.Bytes([]byte{0}), "nil": hff.Null()})}, []string{`data`, `data:has("a")`, `data:get("a"):type="bytes"`, `data:get("missing")=null`, `data:values:contains("AA==":frombase64)`, `data:values:contains(null)`, `data:values:map(x=>x:base64):json=data:values:map(x=>x:string):json`, `data:json="{\"a\":\"AA==\",\"nil\":null}"`, `data:keys:json="[\"a\",\"nil\"]"`}},
			fixture{fixtures[0].typ, storage, fixtures[0].values, append(append([]string{}, fixtures[0].sources...), `[data]:json="[null]"`, `[data]:contains(data)`, `[data]:unique`, `[data]=[data]`, `[data]!=[data:base64]`, `[data]:map(x=>x:base64)[0]=data:base64`)},
			fixture{schema.ListOf(schema.Union(schema.BytesType, schema.NullType)), storage, []hff.Value{hff.List(), hff.List(hff.Bytes(nil)), hff.List(hff.Bytes([]byte{0}), hff.Null(), hff.Bytes([]byte{0})), hff.List(hff.Bytes(all))}, []string{`data`, `data:json="[]"`, `data[0]:type="bytes"`, `data[-1]:len=256`, `data:contains("AA==":frombase64)`, `data:contains(null)`, `data:unique`, `data:map(x=>x:base64):json=data:map(x=>x:string):json`, `data:map(x=>x)=data`, `data:flatmap(x=>[x])=data`, `data:all(x=>x=null|x:type="bytes")`, `data:[list[bytes|null]]:exists`, `data:map(x=>x!):exists`, `data:concat(data):size=data:size*2`, `data:foldl("AA==":frombase64,(a,x)=>a):base64="AA=="`, `data:foldr("AA==":frombase64,(x,a)=>a):base64="AA=="`, `data:foldl(["AA==":frombase64],(a,x)=>a):json="[\"AA==\"]"`, `data:foldr(["AA==":frombase64],(x,a)=>a):json="[\"AA==\"]"`}},
			fixture{schema.Struct(schema.Bytes("raw"), schema.String("text")), storage, []hff.Value{hff.Object(map[string]hff.Value{"raw": hff.Bytes([]byte{0}), "text": hff.String("AA==")})}, []string{`data.raw:type="bytes"`, `data.text:type="string"`, `data:json="{\"raw\":\"AA==\",\"text\":\"AA==\"}"`, `[data.raw]!=[data.text]`, `data.raw:in(data.raw)`, `data.raw:in(data.text)=false`}},
		)
	}

	fixtures = append(fixtures, fixture{schema.Struct(schema.Bytes("raw"), schema.Any("text")), "json", []hff.Value{hff.Object(map[string]hff.Value{"raw": hff.Bytes([]byte{0}), "text": hff.String("AA==")}), hff.Object(map[string]hff.Value{"raw": hff.Bytes([]byte{0}), "text": hff.Float(1.5)})}, []string{
		`[1]:foldl({"b":data.raw,"v":data.text!},(a,x)=>a).b:type="bytes"`,
		`[1]:foldl({"b":data.raw,"v":data.text!},(a,x)=>a).v:type=data.text:type`,
		`[1]:foldl({"v":data.text!},(a,x)=>{"v":a!.v!}):json={"v":data.text!}:json`,
		`[1]:foldl([data.text!],(a,x)=>[a![0]!]):json=[data.text!]:json`,
		`[1]:foldl({"v":data.text!},(a,x)=>{"v":a!:get("v")!}):json={"v":data.text!}:json`,
		`[1,2]:foldl({"a":data.raw,"b":null,"c":null},(a,x)=>{"a":a.c,"b":a.a,"c":a.b}).c:base64=data.raw:base64`,
	}})
	for fi, fixture := range fixtures {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", fixture.typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for si, source := range fixture.sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			f, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			binding := hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))
			if fixture.storage == "json" || fixture.storage == "jsonb" {
				binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONFormat(fixture.storage))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil {
				t.Fatal(source, err)
			}
			var rows, types, args []string
			for _, arg := range q.Args() {
				typ, lit := sqlFixtureParameter(t, arg)
				types, args = append(types, typ), append(args, lit)
			}
			for i, value := range fixture.values {
				want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"data": value}))
				if err != nil {
					t.Fatal(err)
				}
				row := ""
				if fixture.storage == "json" || fixture.storage == "jsonb" {
					row = sqlFixtureString(metadataJSONFixture(t, value)) + "::" + fixture.storage
				} else {
					row = sqlFixtureValue(t, value, fixture.storage)
				}
				rows = append(rows, "("+row+")")
				wants = append(wants, want)
				labels = append(labels, fmt.Sprintf("%s %s input%d", source, fixture.storage, i))
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE bytes_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data); EXECUTE bytes_%d_%d%s;\n", fi, si, parameters, q.Where(), strings.Join(rows, ","), fi, si, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(wants) {
		t.Fatalf("got%d want%d: %s", len(results), len(wants), out)
	}
	for i, result := range results {
		if (result == "t") != wants[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, wants[i])
		}
	}
	t.Logf("compared %d bytes results", len(results))
}
