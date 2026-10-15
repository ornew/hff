package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// A positional pair proof alone does not prove distinct keys. Selection and
// identity retain that stronger invariant; replacement and duplication do not.
func itemUniqueCases() []struct {
	source   string
	pairwise bool
} {
	return []struct {
		source   string
		pairwise bool
	}{
		{`labels:items:unique`, false},
		{`labels!:items:unique`, false},
		{`labels:items!:unique`, false},
		{`labels:items:filter(p=>p[0]="app"):unique`, false},
		{`labels:items:filter(p=>p[0]="missing"):unique`, false},
		{`labels:items:filter(p=>p[1]!:exists):unique`, false},
		{`labels:items:map(p=>p):unique`, false},
		{`labels:items:map(p=>p!):unique`, false},
		{`labels:items:flatmap(p=>[p]):unique`, false},
		{`labels:items:flatmap(p=>[p!]):map(p=>p):unique`, false},
		{`labels:items:filter(p=>p[0]):flatmap(p=>[p]):unique`, false},
		{`labels:items:flatmap(p=>[p,p]):distinct:unique`, false},
		{`labels:items:flatmap(p=>[p,null]):distinct:unique`, false},
		{`labels:items:concat(other:items):distinct:unique`, false},
		{`labels:items:flatmap(p=>[p,p]):unique`, true},
		{`labels:items:flatmap(p=>[p,null]):unique`, true},
		{`labels:items:concat(labels:items):unique`, true},
		{`labels:items:map(p=>other:items[0]):unique`, true},
		{`labels:items:map(p=>other:items[0]!):unique`, true},
		{`labels:items:map(p=>other:items:find(p=>p[0]="app")):unique`, true},
		{`labels:items:flatmap(p=>other:items:map(q=>p)):unique`, true},
	}
}

func itemUniqueSources(fixture itemRoleFixture) []string {
	var sources []string
	for _, tc := range itemUniqueCases() {
		// Mixed String/tagged Struct values still require key-dependent
		// tuple-slot recovery; uniqueness must not broaden that support.
		if fixture.mixed && tc.source == `labels:items:filter(p=>p[1]!:exists):unique` {
			continue
		}
		sources = append(sources, tc.source)
	}
	return sources
}

func TestJSONItemUniquenessProofAvoidsPairwiseComparison(t *testing.T) {
	ctx := context.Background()
	tc := itemRoleFixtures()[0]
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range itemUniqueCases() {
		source := test.source
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format)...))
			if err != nil {
				t.Fatal(source, format, err)
			}
			// distinct() may still compare rows to perform its own operation;
			// unique() uses a separate cross product of array elements.
			pairwise := strings.Contains(q.Where(), "CROSS JOIN json_array_elements(") || strings.Contains(q.Where(), "CROSS JOIN jsonb_array_elements(")
			if want := test.pairwise; pairwise != want {
				t.Errorf("%s %s: pairwise comparison=%t, want %t", source, format, pairwise, want)
			}
			if source == `labels:items:unique` {
				t.Logf("%s direct items unique: %d SQL bytes", format, len(q.Where()))
			}
		}
	}
}

func TestJSONItemUniquenessDoesNotBypassReceiverRecovery(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		object    hff.Type
		source    string
		wantError bool
	}{
		{schema.MapOf(schema.Union(schema.StringType, schema.BytesType)), `labels:items:unique`, true},
		{schema.MapOf(schema.BytesType), `labels:items:map(p=>p:[list]):unique`, false},
		{schema.Struct(schema.String("app"), schema.Bytes("z")), `labels:items:filter(p=>p[1]!:exists):unique`, true},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", test.object))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, test.source)
		if err != nil {
			t.Fatal(test.source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if (err != nil) != test.wantError {
				t.Errorf("%s %s: error=%v, wantError=%t", format, test.source, err, test.wantError)
			}
		}
	}
}
