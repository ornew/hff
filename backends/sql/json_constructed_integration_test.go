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
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLConstructedCallbackJSONMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	fixtures := []struct {
		name         string
		typ          hff.Type
		value, other hff.Value
	}{
		{"Int", schema.IntType, hff.Int(math.MaxInt64), hff.Int(math.MinInt64)},
		{"Float", schema.FloatType, hff.Float(math.SmallestNonzeroFloat64), hff.Float(math.Copysign(0, -1))},
		{"String", schema.StringType, hff.String("東京 / ~"), hff.String("")},
		{"Bool", schema.BoolType, hff.Bool(true), hff.Bool(false)},
		{"Bytes", schema.BytesType, hff.Bytes([]byte{0, 254}), hff.Bytes(nil)},
		{"Duration", schema.DurationType, hff.Duration(time.Second + time.Nanosecond), hff.Duration(-time.Second - time.Nanosecond)},
		{"Timestamp", schema.TimestampType, hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 123, time.UTC)), hff.Timestamp(time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC))},
		{"NumericUnion", schema.Union(schema.IntType, schema.FloatType), hff.Int(9007199254740993), hff.Float(1)},
		{"Container", schema.ListOf(schema.NullableType(schema.FloatType)), hff.List(hff.Float(1), hff.Null()), hff.List()},
		{"Any", schema.AnyType, hff.Int(1), hff.Float(1)},
	}
	sources := []string{
		`items:map(x=>x)=items`, `items:map(needle=>needle)=items`,
		`items:map(x=>needle):size=items:size`, `items:map(x=>x!):exists`,
		`items:map(x=>guard!.value):exists`, `!(items:map(x=>guard!.value)=null)`,
		`items:map(x=>items:map(y=>x)):size=items:size`,
		`items:foldl(needle,(a,x)=>x)=needle`, `items:foldr(needle,(x,a)=>x)=needle`,
		`items:foldl(needle,(a,x)=>a)=needle`, `items:foldr(needle,(x,a)=>a)=needle`,
		`items:foldl(needle,(a,x)=>x!)=null`, `!(items:foldr(needle,(x,a)=>guard!.value)=null)`,
		`items:foldl(needle,(a,x)=>items:foldl(a,(a,y)=>a))=needle`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, fixture := range fixtures {
		typ := schema.NullableType(fixture.typ)
		e, err := engine.New(engine.WithSchema(schema.Struct(
			schema.Field("items", schema.NullableType(schema.ListOf(typ))),
			schema.Field("needle", typ),
			schema.Field("guard", schema.NullableType(schema.Struct(schema.Field("value", typ)))),
		)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		var records []hff.Value
		for _, items := range []hff.Value{hff.List(fixture.value, hff.Null(), fixture.other), hff.List(hff.Null(), fixture.value), hff.List(), hff.Null()} {
			for _, needle := range []hff.Value{fixture.value, hff.Null()} {
				for _, guard := range []hff.Value{hff.Null(), hff.Object(map[string]hff.Value{"value": needle})} {
					records = append(records, hff.Object(map[string]hff.Value{"items": items, "needle": needle, "guard": guard}))
				}
			}
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			var options []hffsql.Option
			var bindings []hffsql.Binding
			for _, field := range []string{"items", "needle", "guard"} {
				var metadata []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					metadata = append(metadata, hffsql.WithTypeMetadata(hffsql.Column("records", field+"_meta")))
				}
				bindings = append(bindings, hffsql.BindJSON(hff.Path{field}, hffsql.Column("records", field), format, metadata...))
			}
			options = append(options, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
			var rows []string
			for i, record := range records {
				values, _ := record.AsObject()
				var row []string
				for _, field := range []string{"items", "needle", "guard"} {
					literal := sqlFixtureString(selectedMapJSONFixture(t, values[field]))
					if values[field].Kind() == hff.NullKind && i%2 == 0 {
						literal = "NULL"
					}
					row = append(row, literal+"::"+string(format))
				}
				for _, field := range []string{"items", "needle", "guard"} {
					row = append(row, sqlFixtureString(pairTypeMetadata(t, values[field]))+"::jsonb")
				}
				rows = append(rows, "("+strings.Join(row, ",")+")")
			}
			for _, source := range sources {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(fixture.name, source, err)
				}
				f, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, options...)
				if err != nil {
					t.Fatal(fixture.name, format, source, err)
				}
				var types, args []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					args = append(args, literal)
				}
				parameters, arguments := "", ""
				if len(types) > 0 {
					parameters = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				for _, digits := range []int{1, 3} {
					index := len(expected)
					fmt.Fprintf(&script, "SET extra_float_digits=%d; PREPARE constructed_%d%s AS SELECT %s FROM (VALUES %s) records(items,needle,guard,items_meta,needle_meta,guard_meta); EXECUTE constructed_%d%s;\n", digits, index, parameters, q.Where(), strings.Join(rows, ","), index, arguments)
					for ri, record := range records {
						want, err := f.Match(ctx, record)
						if err != nil {
							t.Fatal(fixture.name, source, err)
						}
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s %s digits%d %s row%d", fixture.name, format, digits, source, ri))
					}
				}
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
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] || (result != "t" && result != "f") {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
	t.Logf("checked %d constructed callback JSON results", len(expected))
}
