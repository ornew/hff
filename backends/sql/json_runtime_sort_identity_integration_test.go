//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

func TestPostgreSQLRuntimeSortPreservesDeclaredIdentity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	for _, tc := range sortIdentityFixtures() {
		object := func(values map[string]hff.Value) hff.Value { return hff.Object(values) }
		values := []hff.Value{
			hff.Null(), object(nil), object(map[string]hff.Value{"app": hff.String("auth")}),
			object(map[string]hff.Value{"z": tc.first}),
			object(map[string]hff.Value{"x": tc.second, "z": tc.first}),
			object(map[string]hff.Value{"app": hff.String("auth"), "z": tc.first}),
			object(map[string]hff.Value{"app": hff.Int(1), "z": tc.first}),
			object(map[string]hff.Value{"app": hff.Null(), "z": tc.first}),
			object(map[string]hff.Value{"app": hff.Bool(false), "z": tc.first}),
			object(map[string]hff.Value{"app": hff.List(hff.Int(1)), "z": tc.first}),
			object(map[string]hff.Value{"app": object(map[string]hff.Value{"a": hff.Int(1)}), "z": tc.first}),
			object(map[string]hff.Value{"z": hff.Null()}),
			object(map[string]hff.Value{"x": hff.Null(), "z": tc.first}),
			object(map[string]hff.Value{"app": hff.String("auth"), "x": tc.second, "z": tc.first}),
			hff.Value{},
		}
		nullExpected := []bool{true, false, false, !tc.comparable, !tc.comparable, true, true, true, true, true, true, true, true, true, true}
		e, err := engine.New(engine.WithSchema(sortIdentitySchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
				defer cancel()
				var script strings.Builder
				script.WriteString("SET jit=off;SET statement_timeout='20s';\n")
				fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,labels %s,meta jsonb);\n", format)
				var records []hff.Value
				for i, value := range values {
					fields := map[string]hff.Value{}
					raw, meta := "NULL", `{"version":1,"types":{}}`
					if value.Kind() != hff.InvalidKind {
						fields["labels"] = value
						raw = sqlFixtureString(taggedCollectionJSON(t, value))
						meta = pairTypeMetadata(t, value)
					}
					fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s,%s);\n", i, raw, sqlFixtureString(meta))
					records = append(records, object(fields))
				}
				sources := append(sortIdentitySources(), `labels:items:map(p=>p[1]):sort[0]=labels.z`)
				expectedJSONSource := ""
				if tc.comparable {
					expectedJSONSource = `labels:items:map(p=>p[1]):sort:json=` + strconv.Quote(taggedCollectionJSON(t, hff.List(tc.first, tc.second)))
					sources = append(sources, expectedJSONSource)
				}
				var expected []bool
				var labels []string
				for i, source := range sources {
					p, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, p)
					if err != nil {
						t.Fatal(err)
					}
					q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(sortIdentityBinding(format)))
					if err != nil {
						t.Fatal(source, err)
					}
					requireConcatArgsUsed(t, q)
					appendAggregateProfileQuery(t, &script, q, "records ORDER BY id", i)
					for row, record := range records {
						want, err := filter.Match(ctx, record)
						if err != nil {
							t.Fatal(err)
						}
						if source == `labels:values:sort:type="null"` || source == `labels:items:map(p=>p[1]):sort:type="null"` || source == `labels:items:map(p=>p[1]):foldl([],(a,v)=>a:concat([v])):sort:type="null"` {
							if want != nullExpected[row] {
								t.Fatalf("independent null expectation %s row%d got%v want%v", source, row, want, nullExpected[row])
							}
						}
						if row == 4 && (source == `labels:items:map(p=>p[1]):sort[0]=labels.z` || source == expectedJSONSource) && want != tc.comparable {
							t.Fatal("independent homogeneous order expectation", source, want)
						}
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s row%d", source, row))
					}
					var args []string
					for _, arg := range q.Args() {
						_, literal := sqlFixtureParameter(t, arg)
						args = append(args, literal)
					}
					name := fmt.Sprintf("sort_identity_%d", i)
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
}
