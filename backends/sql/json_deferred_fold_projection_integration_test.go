//go:build integration

package sql_test

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLUnusedDeferredInputFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, unusedInputFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLUnusedDeferredFoldExactOutputs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(schema.Optional(schema.Nullable(schema.Bytes("asset"))), schema.Optional(schema.Nullable(schema.String("name"))))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	values := `labels:items:map(p=>p[1])`
	constructed := values + `:map(v=>[v,v])`
	type expectation struct {
		source string
		want   []bool
	}
	records := []string{`'{"asset":"YQ==","name":"Alice"}'`, `'{}'`, `NULL`, `'{"asset":null,"name":"Alice"}'`}
	cases := []expectation{
		{constructed + `:foldl(0,(a,v)=>a+1)=2`, []bool{true, false, false, true}},
		{constructed + `:foldr(0,(v,a)=>a+1)=2`, []bool{true, false, false, true}},
		{constructed + `:foldl("seed",(a,v)=>a:concat("x"))="seedxx"`, []bool{true, false, false, true}},
		{constructed + `:foldl("seed",(a,v)=>"body")="seed"`, []bool{false, true, false, false}},
		{constructed + `:foldl(null,(a,v)=>a!):type:exists`, []bool{false, true, true, false}},
		{constructed + `:foldl(null!,(a,v)=>1):type:exists`, []bool{false, false, true, false}},
		{values + `:map(v=>v!):map(v=>[v]):foldl(0,(a,v)=>a+1):exists`, []bool{true, true, false, false}},
		{values + `:map(v=>v!):flatmap(v=>[]):foldl("seed",(a,v)=>"body"):type:exists`, []bool{true, true, true, false}},
		{constructed + `:foldl(0,(a,v)=>[1,2]:foldl(a,(b,v)=>b+v))=6`, []bool{true, false, false, true}},
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, tc := range cases {
			p, err := e.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			for i, record := range records {
				from := `(VALUES (` + record + `::` + string(format) + `)) records(labels)`
				appendAggregateProfileQuery(t, &script, q, from, len(expected))
				expected = append(expected, tc.want[i])
				labels = append(labels, string(format)+" "+tc.source+" "+record)
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
	t.Logf("%d independent expected results", len(expected))
}

// The typed-parameter parity matrix above does not establish that this
// pre-existing dead-argument boundary works with parameter types omitted.
func TestPostgreSQLUnusedDeferredFoldKnownNoValueArgumentBoundary(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e := unusedFoldEngine(t)
	p, err := e.Compile(ctx, `labels:items:map(p=>p[1])`+knownNoValueFoldSuffix)
	if err != nil {
		t.Fatal(err)
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
		if err != nil {
			t.Fatal(err)
		}
		requireKnownNoValueArgumentBoundary(t, q)
		statement := `SET statement_timeout=30000; PREPARE dead_fold AS SELECT 1 FROM (VALUES ('{"asset":"YQ==","name":"Alice"}'::` + string(format) + `)) records(labels) WHERE ` + q.Where() + ";\n"
		command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
		command.Stdin = strings.NewReader(statement)
		output, err := command.CombinedOutput()
		missing := strconv.Itoa(len(q.Args()) - 1)
		if err == nil || !strings.Contains(string(output), "could not determine data type of parameter $"+missing) {
			t.Fatal("known untyped PREPARE boundary changed", format, err, string(output))
		}
	}
}
