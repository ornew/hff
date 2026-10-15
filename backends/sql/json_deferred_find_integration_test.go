//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
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

func TestPostgreSQLDeferredFindMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredFindSources, deferredValueFixtures()...)
}

func TestPostgreSQLDeferredFindAnyMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, deferredFindAnySources, append(itemAnyFixtures(), runtimeMembershipEdgeFixture())...)
}

func TestPostgreSQLDeferredFindChoiceOrigins(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	for _, tc := range itemRoleFixtures() {
		if !strings.HasSuffix(tc.name, "/map") {
			continue
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", map[string]hff.Type{"bytes": schema.BytesType, "duration": schema.DurationType, "timestamp": schema.TimestampType}[tc.tag]), schema.Field("unknown", schema.AnyType), schema.Int("index"))), engine.WithCapabilities(capabilities.Standard()))
				if err != nil {
					t.Fatal(err)
				}
				var script strings.Builder
				fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,labels %s,unknown %s,unknown_meta jsonb,idx bigint);\n", format, format)
				var records []hff.Value
				labelsValue := hff.Object(map[string]hff.Value{"app": tc.first, "z": tc.second})
				for _, unknown := range []hff.Value{hff.String("YQ=="), hff.String("0.000000001s"), hff.String("2026-01-01T00:00:00.000000123Z"), hff.Int(1), hff.Float(1), hff.Null(), hff.List(hff.Int(1))} {
					for _, index := range []int64{-2, -1, 0, 1, 2} {
						records = append(records, hff.Object(map[string]hff.Value{"labels": labelsValue, "unknown": unknown, "index": hff.Int(index)}))
						fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s::%s,%s::%s,%s::jsonb,%d);\n", len(records), sqlFixtureString(taggedCollectionJSON(t, labelsValue)), format, sqlFixtureString(taggedCollectionJSON(t, unknown)), format, sqlFixtureString(pairTypeMetadata(t, unknown)), index)
					}
				}
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "unknown_meta")))
				}
				bindings := []hffsql.Binding{hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format), hffsql.BindJSON(hff.Path{"unknown"}, hffsql.Column("records", "unknown"), format, options...), hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "idx"))}
				var wants []bool
				var names []string
				queryIndex := 0
				for _, body := range []string{`[v,unknown][index]`, `[unknown,v][index]`, `{"a":v,"b":unknown}["a"]`} {
					for _, observer := range []string{`:find(x=>true):type="` + tc.tag + `"`, `:find(x=>true):type="string"`, `:find(x=>true):string=` + taggedCollectionJSON(t, tc.first), `:find(x=>true):type="null"`, `:find(x=>x:exists):type="` + tc.tag + `"`} {
						source := `labels:items:map(p=>p[1]):map(v=>` + body + `)` + observer
						p, err := e.Compile(ctx, source)
						if err != nil {
							t.Fatal(source, err)
						}
						q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
						if err != nil {
							t.Fatal(source, err)
						}
						filter, err := local.Build(ctx, p)
						if err != nil {
							t.Fatal(err)
						}
						appendAggregateProfileQuery(t, &script, q, "records ORDER BY id", queryIndex)
						queryIndex++
						for i, record := range records {
							want, err := filter.Match(ctx, record)
							if err != nil {
								t.Fatal(err)
							}
							wants = append(wants, want)
							names = append(names, fmt.Sprintf("%s row%d", source, i+1))
						}
					}
				}
				t.Logf("%d query-record results", len(wants))
				runSQLBooleanParityScript(t, ctx, container, script.String(), wants, names)
			})
		}
	}
}
