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

func TestPostgreSQLConstructorRootsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, constructorRootSources, append(itemRoleFixtures(), itemAnyFixtures()...)...)
}

func TestPostgreSQLConstructorRootNativeAndRecoveryMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Bytes("asset")), schema.Nullable(schema.String("name")), schema.List("data", schema.BytesType), schema.Nullable(schema.Duration("elapsed")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	sources := []string{`[asset,name][0]:type="bytes"`, `[asset,name]:contains(name)`, `[asset,name]:map(v=>v:type):json:exists`, `[asset,name]:map(v=>v:json):json=[asset:json,name:json]:json`, `[asset,name]:distinct:size=2`, `[asset,name]=[asset,name]`, `[asset,name]:unique`, `[name,"a"]:contains(name)`, `[data[0],"AA=="]:size>0`, `[elapsed,"1s"]:json`, `[elapsed,("1s":frombase64)]:json`}
	for _, source := range sources {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"asset"}, hffsql.Column("records", "asset")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")), hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSON), hffsql.BindJSON(hff.Path{"elapsed"}, hffsql.Column("records", "elapsed"), hffsql.JSON)))
		if err != nil {
			t.Fatal(source, err)
		}
		for _, pair := range [][2]hff.Value{{hff.Bytes([]byte("a")), hff.String("YQ==")}, {hff.Bytes([]byte{0, 255}), hff.String("日本語")}, {hff.Bytes(nil), hff.String("")}, {hff.Null(), hff.String("YQ==")}, {hff.Bytes([]byte("a")), hff.Null()}, {hff.Null(), hff.Null()}} {
			for _, data := range []hff.Value{hff.List(hff.Bytes([]byte{0})), hff.List()} {
				elapsed := hff.Duration(time.Nanosecond)
				if pair[0].Kind() == hff.NullKind {
					elapsed = hff.Null()
				}
				record := hff.Object(map[string]hff.Value{"asset": pair[0], "name": pair[1], "data": data, "elapsed": elapsed})
				want, err := filter.Match(ctx, record)
				if err != nil {
					t.Fatal(source, err)
				}
				from := fmt.Sprintf("(VALUES (%s,%s,%s::json,%s::json)) records(asset,name,data,elapsed)", sqlFixtureValue(t, pair[0], "bytea"), sqlFixtureValue(t, pair[1], "text"), sqlFixtureString(taggedCollectionJSON(t, data)), sqlFixtureString(taggedCollectionJSON(t, elapsed)))
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, want)
				labels = append(labels, source)
			}
		}
	}
	t.Logf("%d query-record results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLConstructorRootPairOriginsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, tc := range itemRoleFixtures() {
		if !strings.HasSuffix(tc.name, "/map") {
			continue
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", tc.typ), schema.Map("right", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`[left:items[0],right:items[0]]:map(p=>p[1]:type):contains("string")`, `[left:items[0],right:items[0]]:json!:exists`} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				var metadata []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					metadata = append(metadata, hffsql.WithTypeMetadata(hffsql.Column("records", "rhs_meta")))
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "lhs"), format), hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "rhs"), format, metadata...)))
				if err != nil {
					t.Fatal(err)
				}
				for _, left := range []hff.Value{hff.Object(map[string]hff.Value{"app": tc.first}), hff.Object(map[string]hff.Value{"app": hff.Null()}), hff.Object(nil), hff.Null()} {
					for _, value := range []hff.Value{hff.String("YQ=="), hff.Int(1), hff.Float(1), hff.Null()} {
						right := hff.Object(map[string]hff.Value{"app": value})
						want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": left, "right": right}))
						if err != nil {
							t.Fatal(err)
						}
						from := fmt.Sprintf("(VALUES (%s::%s,%s::%s,%s::jsonb)) records(lhs,rhs,rhs_meta)", sqlFixtureString(taggedCollectionJSON(t, left)), format, sqlFixtureString(taggedCollectionJSON(t, right)), format, sqlFixtureString(pairTypeMetadata(t, right)))
						appendAggregateProfileQuery(t, &script, q, from, len(expected))
						expected = append(expected, want)
						labels = append(labels, tc.name+" "+string(format)+" "+source)
					}
				}
			}
		}
	}
	t.Logf("%d query-record results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
