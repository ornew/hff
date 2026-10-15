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

func itemConcatPipelineSources(tc itemRoleFixture) []string {
	left := `labels:items:map(p=>p[1])`
	right := `other:items:map(p=>p[1])`
	joined := left + `:concat(` + right + `)`
	dynamic := `labels:items:map(p=>p[labels:size-1]):concat(other:items:map(p=>p[other:size-1]))`
	sources := []string{
		joined + `:map(v=>v:type):json=` + left + `:map(v=>v:type):concat(` + right + `:map(v=>v:type)):json`,
		joined + `:any(v=>v:type="` + tc.tag + `")`,
		joined + `:all(v=>v:type="` + tc.tag + `")`,
		joined + `:count(v=>v:type="` + tc.tag + `")>0`,
		joined + `:any(v=>v)`,
		joined + `:all(v=>v)`,
		joined + `:count(v=>v)>0`,
		joined + `:map(v=>v:json):size=labels:size+other:size`,
		joined + `:map(v=>v!:[null]:type):type="null"`,
		joined + `:map(v=>v!:[null]:type):exists`,
		left + `:filter(v=>v:exists):concat(` + right + `:filter(v=>v=null)):map(v=>v:type):json:exists`,
		left + `:filter(v=>false):concat(` + right + `):map(v=>v:type):json=` + right + `:map(v=>v:type):json`,
		`labels:items:map(p=>p[1]!):filter(v=>false):concat(` + right + `):any(v=>true)`,
		`labels:items:map(p=>p[1]!):concat(` + right + `):any(v=>true)=null`,
		left + `:concat(other:items:map(p=>p[1]!)):all(v=>false)=null`,
		left + `:concat(other:items:map(p=>p[1]!)):map(v=>v:type):type="null"`,
		left + `:concat([]):concat(` + right + `):count(v=>v:exists)>0`,
		left + `:concat(null):any(v=>true)=null`,
		left + `:concat(null):map(v=>v!:[null]:type):type="null"`,
		`labels:items:map(p=>p[1]!):concat(null):any(v=>true)=null`,
		`[]:concat(` + left + `):concat(` + right + `):map(v=>v:type):json=` + left + `:map(v=>v:type):concat(` + right + `:map(v=>v:type)):json`,
		dynamic + `:map(v=>v:type):contains("string")`,
		dynamic + `:any(v=>v:[string]:exists)`,
		dynamic + `:map(v=>other:items:any(p=>p[1]:type=v:type)):all(v=>v)`,
	}
	if !concatFixtureHasAny(tc) {
		sources = append(sources, itemConcatGuardSource)
	}
	return sources
}

const itemConcatGuardSource = `labels:items:map(p=>p[1]:[string|bytes]:[any]):concat([]):map(v=>v:type):size>0`

func concatFixtureHasAny(tc itemRoleFixture) bool {
	return tc.typ.Kind() == hff.AnyType || strings.Contains(tc.name, "/any-field")
}

func concatDifferentFamilies() []itemRoleFixture {
	var fixtures []itemRoleFixture
	for _, tc := range itemRoleFixtures() {
		if strings.HasSuffix(tc.name, "/map") {
			fixtures = append(fixtures, tc)
		}
	}
	return append(fixtures, itemRoleFixture{tag: "string", typ: schema.NullableType(schema.MapOf(schema.NullableType(schema.StringType))), first: hff.String("YQ==")})
}

func concatDifferentFamilySources() []string {
	joined := `left:items:map(p=>p[index]):concat(right:items:map(p=>p[index]))`
	return []string{
		joined + `:map(v=>v:type):json:exists`,
		joined + `:map(v=>[v:type,v:json]):json:exists`,
		joined + `:map(v=>[v:type,v:json]):json=expected`,
		joined + `:map(v=>v:[any]):filter(v=>true):map(v=>[v:type,v:json]):json=expected`,
		joined + `:map(v=>v!):filter(v=>false):map(v=>v:type):type="null"`,
		joined + `:filter(v=>false):map(v=>v!):map(v=>v:type):json="[]"`,
		joined + `:map(v=>v:type):concat(left:items:map(p=>p[index])):map(v=>v:type):count(v=>v="string")=2`,
		joined + `:any(v=>v:[string]:exists)`,
		joined + `:all(v=>v:[string]:exists)`,
		joined + `:count(v=>v:type="bytes")=1`,
		joined + `:map(v=>v!:[null]:type):type="null"`,
		joined + `:map(v=>right:items:any(p=>p[1]:type=v:type)):all(v=>v)`,
		`left:items:map(p=>p[index]!):concat(right:items:map(p=>p[index])):all(v=>true)=null`,
	}
}

func TestJSONConcatPipelineKeepsDifferentFamiliesSeparate(t *testing.T) {
	ctx := context.Background()
	families := concatDifferentFamilies()
	for i, left := range families {
		for _, right := range families[i+1:] {
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", left.typ), schema.Field("right", right.typ), schema.Int("index"), schema.String("expected"))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range append(concatDifferentFamilySources(), `left:items:map(p=>p[1]):concat(right:items:map(p=>p[1])):map(v=>v):json:exists`) {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
					_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "lhs"), format), hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "rhs"), format), hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "idx")), hffsql.Bind(hff.Path{"expected"}, hffsql.Column("records", "expected"))))
					if strings.Contains(source, ":map(v=>v):json") {
						if err == nil {
							t.Fatal("materialized ambiguous element types were accepted", source)
						}
					} else if err != nil {
						t.Fatal(left.tag, right.tag, source, err)
					}
				}
			}
		}
	}
}

func TestJSONItemConcatPipelinesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemMapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		sources := itemConcatPipelineSources(tc)
		if concatFixtureHasAny(tc) {
			// Native Any cannot infer Bytes from JSON string spelling. Keep
			// this guard rejected rather than guessing a source tag.
			sources = append(sources, itemConcatGuardSource)
		}
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				wantError := concatFixtureHasAny(tc) && source == itemConcatGuardSource
				if (err != nil) != wantError {
					t.Errorf("%s %s %s: %v", tc.name, format, source, err)
				}
			}
		}
	}
}

func TestConcatPipelinePreparesInputsBeforeConsumer(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType), schema.Map("other", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []bool{false, true} {
		right, terminal := `"RIGHT"`, `"TERMINAL"`
		if invalid {
			right, terminal = `"\u0000RIGHT"`, `"\u0000TERMINAL"`
		}
		source := `labels:items:map(p=>p[1]):concat(other:items:map(p=>` + right + `)):map(v=>` + terminal + `):exists`
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON), hffsql.BindJSON(hff.Path{"other"}, hffsql.Column("records", "other"), hffsql.JSON)))
		if invalid {
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, right) {
				t.Fatalf("expected earlier source-stage diagnostic, got %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		rightIndex, terminalIndex := -1, -1
		for i, arg := range q.Args() {
			if arg == "RIGHT" && rightIndex == -1 {
				rightIndex = i
			}
			if arg == "TERMINAL" && terminalIndex == -1 {
				terminalIndex = i
			}
		}
		if rightIndex < 0 || terminalIndex < 0 || rightIndex >= terminalIndex {
			t.Fatalf("source arguments must precede terminal arguments: %v", q.Args())
		}
	}
}
