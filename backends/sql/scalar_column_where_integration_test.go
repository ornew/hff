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

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestPostgreSQLNativeColumnFiltersMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	type family struct {
		left, right string
		values      []hff.Value
		extras      []string
		equality    bool
	}
	families := []family{
		{"i", "j", []hff.Value{hff.Int(math.MinInt64), hff.Int(-9007199254740993), hff.Int(-1), hff.Int(0), hff.Int(1), hff.Int(2), hff.Int(18), hff.Int(9007199254740992), hff.Int(9007199254740993), hff.Int(math.MaxInt64)}, []string{`i:round=j`, `i:ceil<j`, `i:floor>=j`, `i:round:floor=j`, `i+0=j*1`, `i:round:json=i:json`, `items:any(x=>i:exists)`, `items:any(x=>x=i)`}, false},
		{"f", "g", []hff.Value{hff.Float(-math.MaxFloat64), hff.Float(-math.SmallestNonzeroFloat64), hff.Float(math.Copysign(0, -1)), hff.Float(0), hff.Float(math.SmallestNonzeroFloat64), hff.Float(1), hff.Float(1.5), hff.Float(9007199254740992), hff.Float(9223372036854775808), hff.Float(math.MaxFloat64)}, []string{`i=f`, `f=i`, `f:floor>=1`, `f:ceil<2`}, false},
		{"s", "t", []hff.Value{hff.String(""), hff.String("A"), hff.String("a"), hff.String("é"), hff.String("世界"), hff.String("O'Reilly")}, nil, false},
		{"p", "q", []hff.Value{hff.Bool(false), hff.Bool(true)}, nil, true},
		{"b", "c", []hff.Value{hff.Bytes(nil), hff.Bytes([]byte{0}), hff.Bytes([]byte{0, 255}), hff.Bytes([]byte("a")), hff.Bytes([]byte("ab"))}, []string{`b:len>0`, `b:len=0`, `!b:len<2`, `b!:len>0`, `b:len=1.0`}, true},
		{"d", "e", []hff.Value{hff.Duration(time.Duration(math.MinInt64)), hff.Duration(-1), hff.Duration(0), hff.Duration(1), hff.Duration(time.Hour), hff.Duration(time.Duration(math.MaxInt64))}, []string{`d:nanoseconds=i`, `d:nanoseconds>0`, `d:nanoseconds:floor:exists`, `d:nanoseconds:round=e:nanoseconds`, `d!:nanoseconds=i`, `d:nanoseconds:json=i:json`}, false},
	}
	names := []string{"i", "j", "f", "g", "s", "t", "p", "q", "b", "c", "d", "e"}
	physical := []string{"bigint", "bigint", "double precision", "double precision", "text", "text", "boolean", "boolean", "bytea", "bytea", "bigint", "bigint"}
	var script strings.Builder
	var wants []bool
	var labels []string
	for mi, mode := range []string{"required", "nullable", "optional"} {
		e, mappings := nativeColumnEngine(t, mode)
		for fi, family := range families {
			values := append([]hff.Value(nil), family.values...)
			if mode != "required" {
				values = append(values, hff.Null())
			}
			var records []hff.Value
			var rows []string
			for _, left := range values {
				for _, right := range values {
					fields := map[string]hff.Value{
						"i": hff.Int(0), "j": hff.Int(1), "f": hff.Float(1.5), "g": hff.Float(2),
						"s": hff.String(""), "t": hff.String("A"), "p": hff.Bool(false), "q": hff.Bool(true),
						"b": hff.Bytes(nil), "c": hff.Bytes([]byte("a")), "d": hff.Duration(0), "e": hff.Duration(time.Hour),
					}
					fields[family.left], fields[family.right] = left, right
					row := make([]string, len(names))
					for i, name := range names {
						row[i] = sqlFixtureValue(t, fields[name], physical[i])
						if mode == "optional" && fields[name].Kind() == hff.NullKind {
							delete(fields, name)
						}
					}
					fields["items"], fields["data"] = hff.List(hff.Int(0), hff.Int(1)), hff.Object(nil)
					row = append(row, "'[0,1]'::json", "'{}'::json")
					records = append(records, hff.Object(fields))
					rows = append(rows, "("+strings.Join(row, ",")+")")
				}
			}
			sources := []string{family.left + ":exists", "!" + family.left + ":exists", family.left + "!=null", family.left + "=null", family.left + ":exists=(" + family.left + "!=null)", family.left + "!:exists", "!" + family.left + "!:exists"}
			operators := []string{"=", "!=", "<", "<=", ">", ">="}
			if family.equality {
				operators = operators[:2]
			}
			for _, operator := range operators {
				comparison := family.left + operator + family.right
				sources = append(sources, comparison, "!("+comparison+")", "("+comparison+"):type=\"bool\"")
			}
			sources = append(sources, family.extras...)
			for si, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(mode, source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
				if err != nil {
					t.Fatal(mode, source, err)
				}
				for ri, record := range records {
					want, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(mode, source, ri, err)
					}
					wants = append(wants, want)
					labels = append(labels, fmt.Sprintf("%s %s row%d", mode, source, ri))
				}
				var types, arguments []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, arguments = append(types, typ), append(arguments, literal)
				}
				params, args := "", ""
				if len(types) > 0 {
					params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(arguments, ",")+")"
				}
				fmt.Fprintf(&script, "PREPARE native_%d_%d_%d%s AS SELECT COALESCE((%s)::text,'n') FROM (VALUES %s) records(%s,items,data); EXECUTE native_%d_%d_%d%s;\n", mi, fi, si, params, q.Where(), strings.Join(rows, ","), strings.Join(names, ","), mi, fi, si, args)
			}
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
		if result == "n" || (result == "true") != wants[i] {
			t.Errorf("%s: SQL=%s local=%t", labels[i], result, wants[i])
		}
	}
	t.Logf("compared %d native-column filter results", len(results))
}
