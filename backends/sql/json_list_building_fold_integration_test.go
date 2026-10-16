//go:build integration

package sql_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLListBuildingFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, listBuildingFoldSources, deferredValueFixtures()...)
}

func TestPostgreSQLListBuildingFoldExactOutputs(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(schema.Optional(schema.Nullable(schema.Bytes("asset"))), schema.Optional(schema.Nullable(schema.String("name"))))))), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const values = `labels:items:map(p=>p[1])`
	records := []string{`'{"asset":"YQ==","name":"Alice"}'`, `'{}'`, `NULL`, `'{"asset":null,"name":"Alice"}'`, `'{"asset":"","name":""}'`}
	forward := []string{`["YQ==",1,"Alice",1]`, `[]`, `null`, `[null,1,"Alice",1]`, `["",1,"",1]`}
	reverse := []string{`["Alice",1,"YQ==",1]`, `[]`, `null`, `["Alice",1,null,1]`, `["",1,"",1]`}
	cases := []struct {
		source string
		output []string
		match  []bool
	}{
		{values + `:foldl([],(a,v)=>a:concat([v,1])):json=expected`, forward, nil},
		{values + `:foldr([],(v,a)=>[v,1]:concat(a)):json=expected`, forward, nil},
		{values + `:foldl([],(a,v)=>[v,1]:concat(a)):json=expected`, reverse, nil},
		{values + `:foldr([],(v,a)=>a:concat([v,1])):json=expected`, reverse, nil},
		{values + `:foldl([],(a,v)=>a:concat([v,1])):map(v=>v:type):json=expected`, []string{`["bytes","number","string","number"]`, `[]`, `null`, `["null","number","string","number"]`, `["bytes","number","string","number"]`}, nil},
		{values + `:foldl([],(a,v)=>a:concat([null,v,{},[]])):json=expected`, []string{`[null,"YQ==",{},[],null,"Alice",{},[]]`, `[]`, `null`, `[null,null,{},[],null,"Alice",{},[]]`, `[null,"",{},[],null,"",{},[]]`}, nil},
		{values + `:foldl([],(a,v)=>a:concat([v!])):json=expected`, []string{`["YQ==","Alice"]`, `[]`, `null`, `[]`, `["",""]`}, []bool{true, true, true, false, true}},
		{values + `:foldr([],(v,a)=>[v!]:concat(a)):json=expected`, []string{`["YQ==","Alice"]`, `[]`, `null`, `[]`, `["",""]`}, []bool{true, true, true, false, true}},
		{values + `:map(v=>v!):filter(v=>false):foldl([],(a,v)=>a:concat([v])):json=expected`, []string{`[]`, `[]`, `null`, `[]`, `[]`}, []bool{true, true, true, false, true}},
		{values + `:filter(v=>false):foldl([],(a,v)=>a:concat([null!,v])):json=expected`, []string{`[]`, `[]`, `null`, `[]`, `[]`}, nil},
		{values + `:foldl([],(a,v)=>a:concat([[1]:map(a=>a+1),v])):json=expected`, []string{`[[2],"YQ==",[2],"Alice"]`, `[]`, `null`, `[[2],null,[2],"Alice"]`, `[[2],"",[2],""]`}, nil},
		{values + `:foldl([],(a,v)=>a:concat([v,-0.0,5e-324])):map(v=>v:[float]:exists):json=expected`, []string{`[false,true,true,false,true,true]`, `[]`, `null`, `[false,true,true,false,true,true]`, `[false,true,true,false,true,true]`}, nil},
	}
	var script strings.Builder
	var expected []bool
	var descriptions []string
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, tc := range cases {
			p, err := e.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format), hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			requireConcatArgsUsed(t, q)
			for i, record := range records {
				from := `(VALUES (` + record + `::` + string(format) + `,` + sqlFixtureString(tc.output[i]) + `::text)) records(labels,expected)`
				for _, typed := range []bool{false, true} {
					appendNullArgumentQuery(t, &script, q, from, len(expected), typed)
					expected = append(expected, tc.match == nil || tc.match[i])
					descriptions = append(descriptions, tc.source+" "+record)
				}
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, descriptions)
	t.Logf("%d independent typed/inferred list-building outputs", len(expected))
}

func TestPostgreSQLListBuildingFoldCanonicalAnyNumbers(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Any("app"), schema.Bytes("z"))), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	floating, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Float("app"), schema.Bytes("z"))), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []bool
	var descriptions []string
	for _, input := range []struct {
		raw, types    string
		declaredFloat bool
	}{
		{`1e400`, `["null","bytes"]`, false},
		{`1e-400`, `["null","bytes"]`, false},
		// An integer-spelled Float needs a declared Float schema. The same
		// spelling as Any would violate the stored int64 input contract.
		{`9223372036854775808`, `["number","bytes"]`, true},
		{`9223372036854775807`, `["number","bytes"]`, false},
		{`-9223372036854775808`, `["number","bytes"]`, false},
		{`-0.0`, `["number","bytes"]`, false},
		{`5e-324`, `["number","bytes"]`, false},
	} {
		// JSON retains the numeric spelling needed for Any's numeric subtype.
		for _, fold := range []string{`foldl([],(a,v)=>a:concat([v]))`, `foldr([],(v,a)=>[v]:concat(a))`} {
			source := `labels:items:map(p=>p[1]):` + fold + `:map(v=>v:type):json=expected`
			compiler := e
			if input.declaredFloat {
				compiler = floating
			}
			p, err := compiler.Compile(ctx, source)
			if err != nil {
				t.Fatal(err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
			if err != nil {
				t.Fatal(err)
			}
			from := `(VALUES (` + sqlFixtureString(`{"app":`+input.raw+`,"z":"YQ=="}`) + `::json,` + sqlFixtureString(input.types) + `::text)) records(labels,expected)`
			for _, typed := range []bool{false, true} {
				appendNullArgumentQuery(t, &script, q, from, len(expected), typed)
				expected = append(expected, true)
				descriptions = append(descriptions, source+" "+input.raw)
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, descriptions)
	t.Logf("%d independent canonical Any numeric outputs", len(expected))
}
