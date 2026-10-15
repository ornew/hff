//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

func TestPostgreSQLEmptyDeferredConcatMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	e, err := engine.New(engine.WithSchema(emptyConcatSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var records []hff.Value
	for _, data := range []hff.Value{
		hff.Null(), hff.List(), hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Null()),
		hff.List(hff.Float(1.5), hff.Int(-2), hff.Null()),
		hff.List(hff.Int(math.MaxInt64), hff.Int(1)),
		hff.List(hff.Float(math.Copysign(0, -1)), hff.Float(1e308), hff.Float(1e308)),
	} {
		for _, empty := range []hff.Value{hff.Null(), hff.List()} {
			for _, labels := range []hff.Value{hff.Null(), hff.Object(nil), hff.Object(map[string]hff.Value{"a": hff.Bytes([]byte("a")), "b": hff.Null()})} {
				for _, zero := range []hff.Value{hff.Null(), hff.Object(nil)} {
					gate := []hff.Value{hff.Null(), hff.Bool(false), hff.Bool(true)}[len(records)%3]
					records = append(records, hff.Object(map[string]hff.Value{"data": data, "empty": empty, "labels": labels, "zero": zero, "gate": gate}))
				}
			}
		}
	}
	records = append(records, hff.Object(map[string]hff.Value{"gate": hff.Null()}))
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()
			var script strings.Builder
			script.WriteString("SET application_name='hff-empty-deferred-concat';SET jit=off;SET statement_timeout='30s';\n")
			fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s,empty %s,labels %s,zero %s,gate boolean,metadata jsonb);\n", format, format, format, format)
			for i, record := range records {
				parts := []string{fmt.Sprint(i)}
				for _, name := range []string{"data", "empty", "labels", "zero"} {
					value, _ := record.Lookup(name)
					text := "NULL"
					if value.Kind() != hff.InvalidKind && value.Kind() != hff.NullKind {
						text = sqlFixtureString(taggedCollectionJSON(t, value))
					}
					parts = append(parts, text)
				}
				gate, _ := record.Lookup("gate")
				parts = append(parts, "NULL")
				if truth, ok := gate.AsBool(); ok {
					parts[len(parts)-1] = fmt.Sprint(truth)
				}
				data, ok := record.Lookup("data")
				if !ok {
					data = hff.Null()
				}
				parts = append(parts, sqlFixtureString(pairTypeMetadata(t, data)))
				fmt.Fprintf(&script, "INSERT INTO records VALUES(%s);\n", strings.Join(parts, ","))
			}
			var expected []bool
			var labels []string
			for i, source := range emptyDeferredSources() {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(source, err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(emptyConcatBindings(format, format == hffsql.JSONB)...))
				if err != nil {
					t.Fatal(source, err)
				}
				requireConcatArgsUsed(t, q)
				appendAggregateProfileQuery(t, &script, q, "records ORDER BY id", i)
				for row, record := range records {
					want, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(source, row, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s row%d", source, row))
				}
				var args []string
				for _, arg := range q.Args() {
					_, literal := sqlFixtureParameter(t, arg)
					args = append(args, literal)
				}
				name := fmt.Sprintf("empty_concat_%d", i)
				call := name
				if len(args) > 0 {
					call += "(" + strings.Join(args, ",") + ")"
				}
				fmt.Fprintf(&script, "PREPARE %s AS SELECT %s FROM records WHERE id=0;EXECUTE %s;DEALLOCATE %s;\n", name, q.Where(), call, name)
				want, err := filter.Match(ctx, records[0])
				if err != nil {
					t.Fatal(err)
				}
				expected = append(expected, want)
				labels = append(labels, "inferred OID "+source)
			}
			t.Logf("%d query-record results", len(expected))
			runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
		})
	}
}
