package sql_test

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func itemChainSources(tc itemRoleFixture) []string {
	indexes := itemSlotIndexes
	sources := []string{
		`labels:items[0][1]:[any]:[string|null]:type="string"`,
		`labels:items[0][1]:[string]:[string|bytes]:[string]:len>0`,
		`labels:items[0][1]:[string]:[bytes]:exists`,
		`labels:items[0][1]:[string]:[null]=null`,
		`labels:items[0][1]:[null]:[any]:type="null"`,
		`labels:items[0][1]:[null]!:[any]:exists`,
		`!labels:items[0][1]:[string]:[null]:exists`,
		indexes + `:any(i=>labels:items[0][i]:[any]:[string]:len>0)`,
		indexes + `:any(i=>labels:items[0][i]:[null]:[any]:type="null")`,
		indexes + `:any(i=>labels:items[0][i]:[string]:[null]=null)`,
		indexes + `:any(i=>labels!:items[0]![i]:[null]:[any]:type="null")`,
		indexes + `:any(i=>labels:items[0][i!]:[null]:[any]:exists)`,
		`labels:items:any(p=>other:items:any(q=>p[1]:[any]:[string]:type=q[1]:[any]:[string]:type))`,
		`labels:items:any(p=>other:items:any(p=>p[1]:[string]:[string|bytes]:len>0) p[1]:[any]:[string]:len>0)`,
	}
	sources = append(sources, itemKeyChainSources(tc)...)
	if tc.typ.Kind() != hff.AnyType {
		sources = append(sources,
			`labels:items[0][1]:[string|`+tc.tag+`|null]:type="`+tc.tag+`"`,
			`labels:items[0][1]:[string|`+tc.tag+`|null]:exists`,
			`labels:items[0][1]:[string|`+tc.tag+`|null]:[any]:type="`+tc.tag+`"`,
			`labels:items[0][1]:[string|`+tc.tag+`|null]!:[any]:type="`+tc.tag+`"`,
			indexes+`:any(i=>labels:items[0][i]:[string|`+tc.tag+`|null]:[`+tc.tag+`]:type="`+tc.tag+`")`,
			`labels:items:filter(p=>p[1]:[string|`+tc.tag+`|null]:[string]:len>0):size>0`,
			`labels:items:map(p=>p[1]:[string|`+tc.tag+`|null]:[string]:len):size=labels:size`,
			`labels:items[0][1]:[list|object|string|null]:[list[bytes]]:size>0`,
			`labels:items[-1][-1]:[list|object|string|null]:[map[bytes]]:json:exists`,
		)
	}
	return sources
}

func TestJSONItemGuardChainsDoNotEraseEscapingCarriers(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.String("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		source    string
		wantError bool
	}{
		{`labels:items[0][1]:[string|bytes]:type="string"`, false},
		{`labels:items[0][1]:[string|bytes]:exists`, false},
		{`[labels:items[0][1]:[string|bytes]]:map(v=>v:type):size>0`, true},
		{`labels:items:map(p=>p[1]:[string|bytes]:[any]):map(v=>v:type):size>0`, false},
		{`labels:items:map(p=>p[1]):filter(v=>v:[bytes]:exists):map(v=>v):size>0`, true},
		{`labels:items:map(p=>p[1]:[string|bytes]:[any]):concat([]):map(v=>v:type):size>0`, false},
	} {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemSlotSourceBinding(format)))
			if (err != nil) != tc.wantError {
				t.Error(format, tc.source, "error", err, "wantError", tc.wantError)
			}
			if err != nil {
				var diagnostic *hff.Error
				if !errors.As(err, &diagnostic) || diagnostic.Code != hff.BackendError {
					t.Error("rejection bypassed the backend check", format, tc.source, err)
				}
			}
		}
	}
}

func itemKeyChainSources(_ itemRoleFixture) []string {
	return []string{
		`labels:items:map(p=>p[0]:[string|bytes]:[any]):json=keys_json`,
		`labels:items:map(p=>p[0]:[bytes]:[null]):json:exists`,
		`labels:items:any(p=>p[0]:[bytes]:[null]:exists)`,
	}
}

func TestJSONItemGuardChainsBuild(t *testing.T) {
	ctx := context.Background()
	for _, tc := range itemSlotFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range itemChainSources(tc) {
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
