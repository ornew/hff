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
	var script strings.Builder
	var wants []bool
	var labels []string
	type fixture struct {
		typ     hff.Type
		storage string
		values  []hff.Value
		sources []string
	}
	texts := []hff.Value{hff.Null(), hff.String(encoded)}
	for _, s := range []string{"", "AA==", "AAA=", "AAAA", "AB==", "AAB=", "A===", "AA", "AAAA=", "AA==\n", "AA==\r", "AA== ", "-w==", "_w==", "é===", "====", "AAAA\nAAAA"} {
		texts = append(texts, hff.String(s))
	}
	fixtures := []fixture{
		{schema.Union(schema.BytesType, schema.NullType), "bytea", []hff.Value{hff.Null(), hff.Bytes(nil), hff.Bytes([]byte{0}), hff.Bytes(all)}, []string{`data`, `!data`, `data:type="bytes"`, `data:len=256`, `data:base64:string=data:string`, `data:base64:frombase64=data`, `data:base64:frombase64:len=data:len`, `data:json=data:base64:json`, `data!:base64:exists`, `data!:json="null"`, `data:base64=""`, `data:base64:frombase64=null`, `data:exists=([null:frombase64]:json="[null]")`, `data:exists=[(null!):frombase64]:exists`}},
		{schema.Union(schema.StringType, schema.NullType), "text", texts, []string{`data:frombase64`, `data:frombase64=null`, `data:frombase64:base64=data`, `data:frombase64:type="bytes"`, `data:frombase64:len=256`, `data:frombase64:string=""`, `data:frombase64:json="null"`, `data!:frombase64:exists`, `data:frombase64=("AB==":frombase64)`, `data:frombase64=("AA==\n":frombase64)`}},
		{schema.AnyType, "json", append(append([]hff.Value{}, texts...), hff.Int(1), hff.Bool(false), hff.List(), hff.Object(nil)), []string{`data:frombase64=null`, `data:frombase64:base64=data`, `data:frombase64:type="bytes"`, `data!:frombase64:exists`, `data:frombase64:json="null"`}},
		{schema.AnyType, "jsonb", texts, []string{`data:frombase64:base64=data`, `data:frombase64=null`}},
	}
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
