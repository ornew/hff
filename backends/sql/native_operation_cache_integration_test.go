//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
	"unicode"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLNativeOperationReuseMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	base, mappings := nativeColumnEngine(t, "nullable")
	fields := append(base.Schema().Fields(), schema.List("words", schema.NullableType(schema.StringType)))
	e, err := engine.New(engine.WithSchema(schema.Struct(fields...)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	mappings = append(mappings, hffsql.BindJSON(hff.Path{"words"}, hffsql.Column("records", "words"), hffsql.JSON))
	var points strings.Builder
	for _, entry := range unicode.CaseRanges {
		for point := entry.Lo; point <= entry.Hi; point++ {
			points.WriteRune(rune(point))
		}
	}
	texts := []hff.Value{hff.Null(), hff.String(""), hff.String("AbC"), hff.String("abc"), hff.String("İıIißẞ"), hff.String("ΟΣ Σσς"), hff.String("世界🙂"), hff.String(points.String())}
	integers := []hff.Value{hff.Null(), hff.Int(math.MinInt64), hff.Int(math.MinInt64 + 1), hff.Int(-7), hff.Int(-1), hff.Int(0), hff.Int(1), hff.Int(4), hff.Int(7), hff.Int(12), hff.Int(math.MaxInt64)}
	names := []string{"i", "j", "f", "g", "s", "t", "p", "q", "b", "c", "d", "e"}
	physical := []string{"bigint", "bigint", "double precision", "double precision", "text", "text", "boolean", "boolean", "bytea", "bytea", "bigint", "bigint"}
	var records []hff.Value
	var rows []string
	for li, left := range integers {
		for ri, right := range integers {
			values := map[string]hff.Value{
				"i": left, "j": right, "f": hff.Float(1.5), "g": hff.Float(2),
				"s": texts[(li+ri)%len(texts)], "t": texts[(li+ri+1)%len(texts)],
				"p": hff.Bool(li%2 == 0), "q": hff.Bool(ri%2 == 0), "b": hff.Bytes(nil), "c": hff.Bytes([]byte("a")),
				"d": hff.Duration(0), "e": hff.Duration(time.Hour),
			}
			elements := []hff.Value{hff.Int(-1), hff.Int(0), hff.Int(1)}
			if left.Kind() != hff.NullKind {
				elements = append(elements, left)
			}
			if right.Kind() != hff.NullKind {
				elements = append(elements, right)
			}
			items := hff.List(elements...)
			// Include empty sources while also evaluating nullable captures
			// inside nonempty lists whose element type remains strictly Int.
			if li == ri {
				items = hff.List()
			}
			words := hff.List(values["s"], values["t"], hff.Null())
			elapsed := "1ns"
			if (li+ri)%2 == 0 {
				elapsed = "invalid duration"
			}
			data := hff.Object(map[string]hff.Value{"n": left, "text": values["s"], "elapsed": hff.String(elapsed)})
			values["items"], values["data"], values["words"] = items, data, words
			records = append(records, hff.Object(values))
			var row []string
			for i, name := range names {
				row = append(row, sqlFixtureValue(t, values[name], physical[i]))
			}
			for _, value := range []hff.Value{items, data, words} {
				row = append(row, sqlFixtureString(metadataJSONFixture(t, value))+"::json")
			}
			rows = append(rows, "("+strings.Join(row, ",")+")")
		}
	}
	sources := []string{
		`i+2>10 i+2<20`, `i+2=null | i+2>0`, `i+j>0 i+j<20`, `i+j=null | i+j>0`,
		`i-2>0 i-2<20`, `i*2>0 i*2<20`, `i/2>0.0 i/2<20.0`, `i/-2=0.0 (i/-2):json="-0.0"`,
		`i%2=0 i%2!=1`, `i%0=null (i%0):exists`, `i/0=null (i/0):exists`,
		`(-i)>0 (-i)<20`, `(-i)=null | (-i)>0`, `(i+2)*3>10 (i+2)*3<20`,
		`i+2>10 i+3<20`, `i+2>10 i-2<20`, `i+j>0 j+i<20`,
		`i!+2>0 i+2>0`, `i:[int]+2>0 i+2>0`, `i+"2":number>0 i+"2":number<20`,
		`s:lower="abc" s:lower!="def"`, `s:upper="ABC" s:upper!="DEF"`,
		`s:lower=t:lower s:lower:len=t:lower:len`, `s:lower=s:upper`,
		`s:lower:upper="ABC" s:lower:upper!="DEF"`, `s:lower:json=s:lower:json`,
		`s:lower:exists s!:lower:exists`, `s=["A":lower,"A":lower]:json`,
		`items:any(x=>x+2>0 x+2<20)`, `items:any(x=>i+2>0 i+2<20)`,
		`items:any(i=>i+2>0 i+2<20)`, `items:any(x=>x+2>0 items:any(x=>x+2>0 x+2<20))`,
		`items:any(x=>i+2>0) items:any(x=>i+2<20)`,
		`items:map(x=>x+2>0 x+2<20):size>0`,
		`items:foldl(i,(i,x)=>i+x+i+x)>0`, `items:foldr(i,(x,i)=>(i+x)*2+(i+x)*2)>0`,
		`words:any(x=>x:lower="abc" x:lower!="def")`,
		`words:any(s=>s:lower="abc" words:any(s=>s:lower="abc" s:lower!="def"))`,
		`data.n:[int]+2>0 data.n:[int]+2<20`, `data.text:[string]:lower="abc" data.text:[string]:lower!="def"`,
		`i:round:json=i:floor:json`, `i:ceil:floor:round:json=i:json`,
		`d:nanoseconds:json=i:json`, `d!:nanoseconds:json=d:nanoseconds:json`,
		`data.n:[int]:round:json=i:json`, `items:any(x=>x:round+1>i)`,
		`p:and(q):type="bool"`,
		`i:round:json=null:floor:json`,
		`data.elapsed:duration:nanoseconds=1`,
	}
	var script strings.Builder
	var wants []bool
	var labels []string
	for index, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if err != nil {
			t.Fatal(source, err)
		}
		assertOperationArgumentsUsed(t, query)
		positive := 0
		for ri, record := range records {
			want, err := filter.Match(ctx, record)
			if err != nil {
				t.Fatal(source, ri, err)
			}
			if want {
				positive++
			}
			wants, labels = append(wants, want), append(labels, fmt.Sprintf("%s row%d", source, ri))
		}
		if source == `i+2>10 i+2<20` || source == `(i+2)*3>10 (i+2)*3<20` {
			if positive == 0 || positive == len(records) {
				t.Fatal("range control needs matching and nonmatching records", source, positive)
			}
		}
		var types, arguments []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types, arguments = append(types, typ), append(arguments, value)
		}
		parameters, args := "", ""
		if len(types) > 0 {
			parameters, args = "("+strings.Join(types, ",")+")", "("+strings.Join(arguments, ",")+")"
		}
		fmt.Fprintf(&script, "PREPARE reused_operation_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(%s,items,data,words); EXECUTE reused_operation_%d%s;\n", index, parameters, query.Where(), strings.Join(rows, ","), strings.Join(names, ","), index, args)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(wants) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(wants), out)
	}
	for index, result := range results {
		if (result != "true" && result != "false") || (result == "true") != wants[index] {
			t.Errorf("%s: SQL=%s local=%t", labels[index], result, wants[index])
		}
	}
	t.Logf("compared %d native operation reuse results", len(results))
}
