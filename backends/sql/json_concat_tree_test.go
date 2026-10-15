package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func itemConcatTreeSources(tc itemRoleFixture) []string {
	left := `labels:items:map(p=>p[1])`
	right := `other:items:map(p=>p[1])`
	inner := left + `:concat(` + right + `)`
	deferred := inner + `:map(v=>v:[any])`
	return []string{
		deferred + `:concat(` + left + `):map(v=>v:type):json=` + left + `:map(v=>v:type):concat(` + right + `:map(v=>v:type)):concat(` + left + `:map(v=>v:type)):json`,
		left + `:concat(` + deferred + `):map(v=>v:type):contains("` + tc.tag + `")`,
		inner + `:map(v=>v!):filter(v=>false):concat(` + left + `):all(v=>true)=null`,
		inner + `:map(v=>v!):filter(v=>false):concat(null):all(v=>true)=null`,
		inner + `:filter(v=>v:exists):concat(` + left + `):map(v=>v:type):json:exists`,
		left + `:concat(` + inner + `:map(v=>v!):filter(v=>false)):any(v=>true)=null`,
		inner + `:filter(v=>false):map(v=>v!):concat(` + right + `):count(v=>true)=other:size`,
		left + `:concat(null):map(v=>v!):concat(` + right + `):all(v=>true)=null`,
		`labels:items:map(p=>p[1]!):concat(null):map(v=>v:[null]):concat(` + right + `):all(v=>true)=null`,
		left + `:concat(` + right + `:concat(null):map(v=>v!)):any(v=>true)=null`,
		left + `:concat(` + right + `:concat(` + left + `):map(v=>v:[any])):filter(v=>v:exists):map(v=>v:json):json:exists`,
		deferred + `:concat(` + inner + `:filter(v=>v:[string]:exists):map(v=>v:[any])):map(v=>v:type):all(v=>v!="null")`,
		`labels:items:concat(other:items):map(p=>p[labels:size-1]):concat(` + left + `):map(v=>v:type):json:exists`,
		deferred + `:concat(` + right + `):map(v=>other:items:any(p=>p[1]:type=v:type)):all(v=>v)`,
	}
}

func TestJSONConcatTreesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemMapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemConcatTreeSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestConcatTreeBuildFollowsExpressionOrder(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType), schema.Map("other", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []bool{false, true} {
		first, next := `"INNER"`, `"RIGHT"`
		if invalid {
			first, next = `"\u0000INNER"`, `"\u0000RIGHT"`
		}
		source := `labels:items:map(p=>p[1]):concat(labels:items:map(p=>p[1])):map(v=>` + first + `):concat(other:items:map(p=>` + next + `)):map(v=>v:type):exists`
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.BindJSON(hff.Path{"other"}, hffsql.Column("records", "other"), hffsql.JSON)))
		if invalid {
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, first) {
				t.Fatalf("expected left subtree's earlier diagnostic, got %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		firstIndex, nextIndex := -1, -1
		for i, arg := range q.Args() {
			if arg == "INNER" && firstIndex < 0 {
				firstIndex = i
			}
			if arg == "RIGHT" && nextIndex < 0 {
				nextIndex = i
			}
		}
		if firstIndex < 0 || nextIndex < 0 || firstIndex >= nextIndex {
			t.Fatalf("left subtree must finish before the right source: %v", q.Args())
		}
	}
}
