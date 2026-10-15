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
)

func repeatedExpressionSources(itemRoleFixture) []string {
	var sources []string
	for _, value := range []string{
		`p[1]`, `p[-1]`, `p[labels:size-1]`, `p[1]!`, `p[1]:[bytes|null]`, `p[1]:[string|null]`,
		`p[1]:type`, `p[1]:json`, `p[1]:exists`, `p[0]:len+1`, `p[0]:upper`,
		`p[1]=null | p[0]="app"`,
	} {
		for _, constructor := range []string{`[` + value + `,` + value + `]`, `[` + value + `]:concat([` + value + `])`} {
			left := `labels:items:flatmap(p=>` + constructor + `)`
			// Materialize the projection before repeating it on the reference
			// path, so it does not reuse the new expression-identity proof.
			right := `labels:items:map(p=>` + value + `)`
			for _, observer := range []string{`type`, `json`} {
				sources = append(sources, left+`:map(v=>v:`+observer+`):json=`+right+`:map(v=>v:`+observer+`):flatmap(v=>[v,v]):json`)
			}
		}
	}
	for _, constructor := range []string{
		`[[p[1]],[p[1]]]`, `[{"value":p[1]},{"value":p[1]}]`,
		`[[[p[1]]],[[p[1]]]]`,
	} {
		left := `labels:items:flatmap(p=>` + constructor + `)`
		for _, suffix := range []string{`:map(v=>v:json):json:exists`, `:find(v=>true):json:exists`, `:foldl("",(a,v)=>a:concat(v:type)):exists`} {
			sources = append(sources, left+suffix)
		}
	}
	sources = append(sources,
		`labels:items:flatmap(p=>[p[1],p[1]]):flatmap(v=>[v:json,v:json]):json:exists`,
		`labels:items:filter(p=>false):flatmap(p=>[p[1]!,p[1]!]):all(v=>true)`,
		`labels:items:flatmap(p=>[p[1]!,p[1]!]):filter(v=>false):all(v=>true)`,
		`labels:items:flatmap(p=>[p[1]:[bytes],p[1]:[bytes]]):map(v=>v:[any]):json:exists`,
		`labels:items:flatmap(p=>other:items:flatmap(q=>[p[1],q[1]])):map(v=>v:json):json:exists`,
	)
	for _, constructor := range []string{
		`[p[1],p[0],p[1]]`, `[p[1],p[0],p[1],p[0]]`,
		`[p[1],[],p[0],p[1],null]`, `[p[1]!,p[0],p[1]!]`,
		`[p[1]]:concat([p[0],p[1]])`,
	} {
		list := `labels:items:flatmap(p=>` + constructor + `)`
		for _, suffix := range []string{`:map(v=>v:type):json:exists`, `:map(v=>v:json):json:exists`, `:find(v=>true):json:exists`, `:foldr("",(v,a)=>v:type:concat(a)):exists`} {
			sources = append(sources, list+suffix)
		}
	}
	for _, body := range []string{
		`[v:exists,v,v:exists][0]`, `[v:exists,v,v:exists][2]`,
		`[v:exists,v,v:exists][-1]`, `[v:exists,v,v:exists][labels:size-2]`,
		`[v:exists,v,v:exists]:[list[any]][0]`,
		`[v:exists,v,v:exists]:concat([])[0]`,
		`[]:concat([v:exists,v,v:exists])[-1]`,
	} {
		sources = append(sources, `labels:items:map(p=>p[1]):flatmap(v=>[`+body+`]):map(x=>x:json):json:exists`)
	}
	return sources
}

func TestRepeatedJSONLiteralIndexKeepsDirectSelection(t *testing.T) {
	ctx := context.Background()
	tc := flatmapPipelineFixtures()[0]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, position := range []string{"0", "2", "-1"} {
		source := `labels:items:map(p=>p[1]):flatmap(v=>[[v:exists,v,v:exists][` + position + `]]):map(x=>x:json):json:exists`
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, tc.metadata)...))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(q.Where(), "UNION ALL") || len(q.Args()) != 1 {
			t.Fatal("literal selection expanded into row alternatives", position, q.Where(), q.Args())
		}
	}
}

func TestRepeatedJSONExpressionKeepsValidationOrder(t *testing.T) {
	ctx := context.Background()
	tc := flatmapPipelineFixtures()[0]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []bool{false, true} {
		early := `"SOURCE"`
		if invalid {
			early = `"\u0000SOURCE"`
		}
		value := `p[1]:json:concat(` + early + `)`
		source := `labels:items:flatmap(p=>[` + value + `,` + value + `]):map(v=>"TERMINAL"):exists`
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, tc.metadata)...))
		if invalid {
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, early) {
				t.Fatal("changed first diagnostic", source, err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		earlyIndex, lateIndex, occurrences := -1, -1, 0
		for i, arg := range q.Args() {
			if arg == "SOURCE" {
				earlyIndex = i
				occurrences++
			}
			if arg == "TERMINAL" {
				lateIndex = i
			}
		}
		if earlyIndex < 0 || lateIndex <= earlyIndex || occurrences != 1 {
			t.Fatal("discarded arguments leaked or changed evaluation order", q.Args())
		}
		requireConcatArgsUsed(t, q)
	}
}

func repeatedExpressionRestriction(tc itemRoleFixture, source string) bool {
	return repeatedExpressionDiagnostic(tc, source) != ""
}

func repeatedExpressionDiagnostic(tc itemRoleFixture, source string) string {
	if strings.Contains(source, `[]:concat([v:exists,v,v:exists])`) && (tc.tag == "bytes" || tc.tag == "duration" || tc.tag == "timestamp" || tc.name == "list-bytes/any-field") {
		// Empty concat on the left already requires a materialized dynamic
		// receiver on this path; retain its established Build restriction.
		if tc.mixed {
			return "JSON cannot distinguish String, Bytes, Duration, and Timestamp at one structural position"
		}
		return "JSON decoding cannot preserve dynamic Bytes, Duration, or Timestamp type identity"
	}
	if strings.Contains(source, `p[1]:[bytes`) && (tc.name == "native/any" || strings.Contains(tc.name, "/any-field")) {
		return "dynamic JSON guard cannot recover this HFF type"
	}
	return ""
}

func acceptedRepeatedExpressionSources(tc itemRoleFixture) []string {
	var sources []string
	for _, source := range repeatedExpressionSources(tc) {
		if !repeatedExpressionRestriction(tc, source) {
			sources = append(sources, source)
		}
	}
	return sources
}

func TestRepeatedJSONExpressionsBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range flatmapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range repeatedExpressionSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if repeatedExpressionRestriction(tc, source) {
					if err == nil {
						t.Fatal("missing representation diagnostic", tc.name, format, source)
					}
					requireCode(t, err, hff.BackendError)
					if err == nil || !strings.Contains(err.Error(), repeatedExpressionDiagnostic(tc, source)) {
						t.Error("changed representation diagnostic", tc.name, source, err)
					}
					continue
				}
				if err != nil {
					t.Error(tc.name, format, source, err)
					continue
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}
