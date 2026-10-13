//go:build integration

package sql_test

import (
	"context"
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

func TestPostgreSQLLiteralConversionsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, options := literalConversionEngine(t)
	fields := []string{"age", "flag", "name", "blob", "elapsed", "ratio"}
	records := [][]hff.Value{
		{hff.Int(20), hff.Bool(true), hff.String("trueYQ=="), hff.Bytes([]byte("a")), hff.Duration(time.Hour * 2), hff.Float(2)},
		{hff.Int(18), hff.Bool(false), hff.String("42"), hff.Bytes([]byte("b")), hff.Duration(time.Hour), hff.Float(1.5)},
		{hff.Int(0), hff.Bool(false), hff.String("null"), hff.Bytes(nil), hff.Duration(0), hff.Float(0)},
		{hff.Int(-1), hff.Bool(true), hff.String("false"), hff.Bytes([]byte{0, 255}), hff.Duration(-time.Second), hff.Float(-1)},
		{hff.Int(9223372036854775807), hff.Bool(true), hff.String("2026-01-01T03:00:00Z"), hff.Bytes([]byte("ab")), hff.Duration(1), hff.Float(1)},
		{hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null()},
	}
	var rows []string
	for _, record := range records {
		var values []string
		for i, value := range record {
			code := ""
			switch value.Kind() {
			case hff.NullKind:
				code = []string{"NULL::bigint", "NULL::boolean", "NULL::text", "NULL::bytea", "NULL::bigint", "NULL::double precision"}[i]
			case hff.IntKind:
				v, _ := value.AsInt()
				code = fmt.Sprintf("'%d'::bigint", v)
			case hff.BoolKind:
				v, _ := value.AsBool()
				code = fmt.Sprint(v)
			case hff.StringKind:
				v, _ := value.AsString()
				code = sqlFixtureString(v) + "::text"
			case hff.BytesKind:
				v, _ := value.AsBytes()
				code = fmt.Sprintf("decode('%x','hex')", v)
			case hff.DurationKind:
				v, _ := value.AsDuration()
				code = fmt.Sprintf("'%d'::bigint", v)
			case hff.FloatKind:
				v, _ := value.AsFloat()
				code = fmt.Sprintf("'%g'::double precision", v)
			}
			values = append(values, code)
		}
		rows = append(rows, "("+strings.Join(values, ",")+")")
	}
	var sources []string
	for _, text := range []string{"true", "false", "TRUE", "", " true"} {
		literal := fmt.Sprintf("%q:bool", text)
		sources = append(sources, "flag="+literal, "flag!="+literal, "!flag="+literal, "("+"flag="+literal+"):type=\"bool\"")
	}
	for _, text := range []string{"18", "-1", "-0", "9223372036854775807", "9223372036854775808", "-9223372036854775809", "01", "+1", "1 ", "bad", "", "1.5"} {
		sources = append(sources, "age>"+fmt.Sprintf("%q:number", text), "age!="+fmt.Sprintf("%q:number", text))
	}
	for _, text := range []string{"1h", "0.25h0.75h", ".0000000005s.0000000005s", ".0000000005s", "+0", "-1s", "9223372036854775807ns", "9223372036854775808ns", "-9223372036854775808ns", "1d", "bad"} {
		sources = append(sources, "elapsed>"+fmt.Sprintf("%q:duration", text), "elapsed!="+fmt.Sprintf("%q:duration", text))
	}
	for _, text := range []string{"", "YQ==", "YQ", "YR==", "YQ==\n", "YQ==\r", "AA==", "YWI=", "_w=="} {
		for _, method := range []string{"frombase64", "frombase64r"} {
			sources = append(sources, "blob="+fmt.Sprintf("%q:%s", text, method), "blob!="+fmt.Sprintf("%q:%s", text, method))
		}
	}
	sources = append(sources,
		`age>"18":number flag="true":bool name=42:string`,
		`name:contains(true:string,"YQ==":frombase64:base64)`, `name:contains("bad":bool:string,null)`,
		`name:startswith("YQ":frombase64r:base64r)`, `name:len>"2":number`,
		`age:in("18":number,"21":number,"bad":number)`, `flag:in("bad":bool)`,
		`blob:in("YQ":frombase64r,"Yg==":frombase64)`, `elapsed:in("1h":duration,"0":duration)`,
		`name=null:string`, `name="bad":duration:string`, `name="bad":timestamp:string`,
		`name="2026-01-01T12:00:00+09:00":timestamp:string`, `blob elapsed`, `!blob|!elapsed`,
		`ratio>"1.5":number`, `flag=null:bool`, `flag!=null:bool`,
		`flag=(null:bool=null)`, `flag=("bad":bool=null)`, `flag=("bad":bool):exists`,
		`flag:in("true":bool!)`, `flag:in("bad":bool!)`,
		`name=("bad":bool!):string`, `flag=("bad":bool!):exists`,
		`age=("18":number):[float]`, `!age=("18":number):[float]`,
		`name="\u0000":timestamp:string`,
	)
	var script strings.Builder
	var expected []bool
	var labels []string
	for si, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		f, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, options...)
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, value := range q.Args() {
			typ, literal := sqlFixtureParameter(t, value)
			types, args = append(types, typ), append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
		}
		fmt.Fprintf(&script, "PREPARE conversion_%d%s AS SELECT %s FROM (VALUES %s) records(age,flag,name,blob,elapsed,ratio); EXECUTE conversion_%d%s;\n", si, parameters, q.Where(), strings.Join(rows, ","), si, arguments)
		for ri, record := range records {
			values := make(map[string]hff.Value)
			for i, field := range fields {
				values[field] = record[i]
			}
			want, err := f.Match(ctx, hff.Object(values))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s record%d", source, ri))
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d literal-conversion comparisons", len(expected))
}

func TestPostgreSQLLiteralConversionPreservesNullCollectionSearch(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("flags", schema.NullableType(schema.BoolType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	records := []hff.Value{hff.List(), hff.List(hff.Bool(true)), hff.List(hff.Null()), hff.List(hff.Bool(false), hff.Null()), hff.Null()}
	var rows []string
	for _, value := range records {
		rows = append(rows, "("+sqlFixtureString(metadataJSONFixture(t, value))+"::jsonb)")
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, source := range []string{
		`flags:contains("bad":bool)`, `flags=["bad":bool]`, `flags:map(x=>"bad":bool)=[null]`,
		`flags:contains("bad":bool!)`, `flags=["bad":bool!]`, `flags:in(["bad":bool])`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		f, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"flags"}, hffsql.Column("records", "flags"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
		if len(q.Args()) != 0 {
			t.Fatal("failed conversion retained unused input argument", source, q.Args())
		}
		fmt.Fprintf(&script, "SELECT %s FROM (VALUES %s) records(flags);\n", q.Where(), strings.Join(rows, ","))
		for ri, value := range records {
			want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"flags": value}))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s record%d", source, ri))
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d collection null-search comparisons", len(expected))
}
