package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
)

func repeatedFlatmapSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	var sources []string
	for _, body := range []string{`[v,v]`, `[v,v,v]`, `[v]:concat([v])`, `[v,v]:concat([v])`, `[v]:concat([v]:concat([v,v]))`} {
		list := values + `:flatmap(v=>` + body + `)`
		for _, suffix := range []string{
			`:map(v=>v:type):json:exists`,
			`:map(v=>v:json):json:exists`,
			`:find(v=>v:exists):json:exists`,
			`:distinct:map(v=>v:type):json:exists`,
			`:map(v=>v:type):distinct:json:exists`,
			`:foldl("",(acc,v)=>acc:concat(v:type)):exists`,
			`:foldr("",(v,acc)=>v:type:concat(acc)):exists`,
		} {
			sources = append(sources, list+suffix)
		}
	}
	for _, body := range []string{`[v,v,v]`, `[v]:concat([v,v])`} {
		for _, index := range []string{"0", "1", "2", "-1", "-2", "-3", "3", "-4"} {
			sources = append(sources, values+`:flatmap(v=>[`+body+`[`+index+`]]):map(v=>v:json):json:exists`)
		}
	}
	for _, suffix := range []string{
		`:flatmap(v=>[v,v]):flatmap(v=>[v,v]):map(v=>v:json):json:exists`,
		`:map(v=>v!):flatmap(v=>[v,v]):map(v=>v:type):json:exists`,
		`:filter(v=>false):flatmap(v=>[v,v]):map(v=>v:type):json:exists`,
		`:flatmap(v=>[v,v]:concat([])):map(v=>v:json):json:exists`,
		`:flatmap(v=>[v,v!]):map(v=>v:type):json:exists`,
		`:flatmap(v=>[v]:flatmap(u=>[u,v])):map(v=>v:json):json:exists`,
		`:flatmap(v=>[v]:flatmap(v=>[v,v])):map(v=>v:json):json:exists`,
		`:flatmap(v=>other:items:map(p=>p[1]):flatmap(u=>[u,v])):map(v=>v:json):json:exists`,
	} {
		sources = append(sources, values+suffix)
	}
	return sources
}

// These pre-existing representation limits were checked with sharing disabled.
// Preserve the rejection instead of silently extending distinct() support.
func repeatedFlatmapRestriction(tc itemRoleFixture, source string) bool {
	if !strings.Contains(source, "):distinct:map(") {
		return false
	}
	for _, name := range []string{
		"bytes/mixed", "duration/mixed", "timestamp/mixed", "bytes/any-field", "bytes/any-field/nullable-any",
		"duration/any-field", "duration/any-field/nullable-any", "timestamp/any-field", "timestamp/any-field/nullable-any", "list-bytes/any-field",
	} {
		if tc.name == name {
			return true
		}
	}
	return strings.Contains(source, "]:concat([") && (tc.tag == "bytes" || tc.tag == "duration" || tc.tag == "timestamp")
}

func acceptedRepeatedFlatmapSources(tc itemRoleFixture) []string {
	var sources []string
	for _, source := range repeatedFlatmapSources(tc) {
		if !repeatedFlatmapRestriction(tc, source) {
			sources = append(sources, source)
		}
	}
	return sources
}

func TestRepeatedJSONFlatmapPipelinesBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range flatmapPipelineFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range repeatedFlatmapSources(tc) {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if repeatedFlatmapRestriction(tc, source) {
					requireCode(t, err, hff.BackendError)
					if err == nil || !strings.Contains(err.Error(), "JSON cannot distinguish String, Bytes, Duration, and Timestamp") {
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

func TestRepeatedJSONFlatmapSharesEnclosingRows(t *testing.T) {
	tc := flatmapPipelineFixtures()[0]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`[v,v]`, `[v,v,v]`, `[v]:concat([v])`, `[v,v]:concat([v])`} {
		source := `labels:items:map(p=>p[1]):flatmap(v=>` + body + `):map(v=>v:type):json:exists`
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, tc.metadata)...))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(q.Where(), "UNION ALL") || !strings.Contains(q.Where(), "(VALUES (0),(1)") {
			t.Fatal("repeated constructor still duplicates row branches", body, q.Where())
		}
	}
}
