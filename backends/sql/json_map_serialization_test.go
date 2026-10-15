package sql_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func guardedMapJSONFixtures() []itemRoleFixture {
	fixtures := unconstrainedItemGuardFixtures()
	object := func(app, value hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"app": app, "z": value})
	}
	return append(fixtures, itemRoleFixture{
		name: "mixed/bytes-float", tag: "bytes", metadata: true, mixed: true,
		typ:   schema.NullableType(schema.Struct(schema.Optional(schema.Nullable(schema.String("app"))), schema.Optional(schema.Field("z", schema.NullableType(schema.Union(schema.BytesType, schema.FloatType)))))),
		first: hff.Bytes([]byte("a")), second: hff.Float(1.5), zero: hff.Float(math.Copysign(0, -1)),
		extraPairs: [][2]hff.Value{
			{object(hff.String("auth"), hff.Float(math.Copysign(0, -1))), object(hff.String("auth"), hff.Float(0))},
			{object(hff.Null(), hff.Float(1)), object(hff.String("auth"), hff.Bytes([]byte("a")))},
		},
	})
}

func guardedMapJSONSources(itemRoleFixture) []string {
	var sources []string
	for _, receiver := range []string{
		`labels:items`, `labels!:items`, `labels:items:[list[list]]`,
		`labels:items:[list[list]]:[list[list]]`,
		`labels:items:filter(p=>true)`, `labels:items:map(p=>p:[list])`,
	} {
		for _, body := range []string{`p[1]`, `p[-1]`, `p:[list][1]`, `p:[list]![1]`} {
			sources = append(sources, receiver+`:map(p=>`+body+`):json=values_json`)
		}
	}
	return append(sources,
		`labels:items:map(p=>p:[list]![1]):map(v=>v):json=values_json`,
		`labels:items:map(p=>p:[list]![1]):map(v=>v:[any]):json=values_json`,
		`labels:items:map(p=>p:[list][1]!):json:exists`,
		`labels:items:map(p=>p:[list][1]:[string]):json:exists`,
		`labels:items:map(p=>p:[list][1]):filter(v=>v:exists):json:exists`,
		`labels:items:filter(p=>p[0]="app"):map(p=>p:[list][1]):json:exists`,
		`labels:items:map(p=>p:[list][1]):map(v=>"kept"):json:exists`,
		`labels:items:any(p=>labels:items:map(q=>q:[list][1]):json:contains(p[0]))`,
		`labels:items:any(p=>other:items:map(p=>p:[list][1]):json:exists p:[list][0]="app")`,
	)
}

func TestGuardedMapJSONBuild(t *testing.T) {
	for _, tc := range guardedMapJSONFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range guardedMapJSONSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Error(tc.name, format, source, err)
				} else {
					requireConcatArgsUsed(t, q)
				}
			}
		}
	}
}

func TestGuardedMapJSONRejectsAmbiguousOrigins(t *testing.T) {
	for _, typ := range []hff.Type{
		schema.MapOf(schema.Union(schema.StringType, schema.BytesType)),
		schema.Struct(schema.Field("app", schema.Union(schema.StringType, schema.DurationType)), schema.Timestamp("z")),
		schema.Struct(schema.String("app"), schema.Float("z")),
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(context.Background(), `labels:items:map(p=>p:[list]![1]):json:exists`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			fields := typ.Fields()
			if len(fields) == 2 && fields[1].Type.Kind() == hff.FloatType && format == hffsql.JSON {
				// Native JSON preserves Float identity without metadata.
				if err != nil {
					t.Error("rejected recoverable Float", err)
				} else {
					requireConcatArgsUsed(t, q)
				}
				continue
			}
			var diagnostic *hff.Error
			if !errors.As(err, &diagnostic) || diagnostic.Code != hff.BackendError || q != nil {
				t.Error("accepted unrecoverable origin", typ.Key(), format, q, err)
			}
		}
	}
}

func TestGuardedMapJSONReportsDeferredSourceFailure(t *testing.T) {
	e, err := engine.New(engine.WithSchema(itemRoleSchema(itemRoleFixtures()[2])), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const source = `labels:items:map(p=>p:[list][1]):map(v=>"\u0000FIRST"):json="\u0000SECOND"`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON)...))
	var diagnostic *hff.Error
	if !errors.As(err, &diagnostic) || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000FIRST"`) || q != nil {
		t.Fatal("source failure must precede observer argument", q, err)
	}
}

func TestGuardedMapJSONCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(itemRoleSchema(itemRoleFixtures()[2])), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p:[list]![1]):json=values_json`)
	if err != nil {
		t.Fatal(err)
	}
	opts := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, opts...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, opts...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatal(q, err)
		}
	}
}
