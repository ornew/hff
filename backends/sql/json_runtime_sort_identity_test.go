package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type sortIdentityFixture struct {
	name          string
	typ           hff.Type
	first, second hff.Value
	comparable    bool
}

func sortIdentityFixtures() []sortIdentityFixture {
	return []sortIdentityFixture{
		{"bytes", schema.BytesType, hff.Bytes([]byte("a")), hff.Bytes([]byte("z")), false},
		{"duration", schema.DurationType, hff.Duration(2 * time.Second), hff.Duration(10 * time.Second), true},
		{"timestamp", schema.TimestampType, hff.Timestamp(time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC)), hff.Timestamp(time.Date(1970, 1, 1, 0, 0, 0, 1, time.UTC)), true},
	}
}

func sortIdentitySchema(tc sortIdentityFixture) hff.Type {
	return schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(
		schema.Optional(schema.Field("app", schema.AnyType)),
		schema.Optional(schema.Nullable(schema.Field("x", tc.typ))),
		schema.Optional(schema.Nullable(schema.Field("z", tc.typ))),
	)))))
}

func sortIdentityBinding(format hffsql.JSONFormat) hffsql.Binding {
	var opts []hffsql.JSONBindingOption
	if format == hffsql.JSONB {
		opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
	}
	return hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, opts...)
}

func sortIdentitySources() []string {
	const direct = `labels:items:map(p=>p[1])`
	var sources []string
	for _, prefix := range []string{`labels:values`, `[labels.app,labels.z]`, direct, direct + `:foldl([],(a,v)=>a:concat([v]))`, direct + `:concat([])`, direct + `:filter(v=>true)`, direct + `:map(v=>v:[any])`} {
		for _, suffix := range []string{`:sort:type="null"`, `:sort:exists`, `:sort:json:exists`, `:sort:map(v=>v:type):json:exists`, `:sort:sort:json=` + prefix + `:sort:json`, `:sort[0]:type="bytes"`, `:sort[-1]:type="duration"`, `:sort:map(v=>v!):exists`} {
			sources = append(sources, prefix+suffix)
		}
	}
	return append(sources,
		direct+`:sort:json=`+direct+`:foldl([],(a,v)=>a:concat([v])):sort:json`,
		`labels:items:any(p=>`+direct+`:sort:map(v=>v:in(p[1])):all(v=>v))`,
		`labels!:items:map(p=>p[1]!):sort:type="null"`,
	)
}

func TestRuntimeSortIdentityKeepsTaggedArms(t *testing.T) {
	for _, tc := range sortIdentityFixtures() {
		e, err := engine.New(engine.WithSchema(sortIdentitySchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):sort:type="null"`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(sortIdentityBinding(format)))
			if err != nil {
				t.Fatal(tc.name, err)
			}
			if !strings.Contains(q.Where(), "json_build_array('"+tc.name+"'::text,") {
				t.Error("sort erased original tagged identity", tc.name, format)
			}
			requireConcatArgsUsed(t, q)
		}
	}
}

func TestRuntimeSortIdentityCancellation(t *testing.T) {
	e, err := engine.New(engine.WithSchema(sortIdentitySchema(sortIdentityFixtures()[0])), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):sort:type="null"`)
	if err != nil {
		t.Fatal(err)
	}
	opts := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))}
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

func TestRuntimeSortIdentityChecksReceiverBeforeObserver(t *testing.T) {
	e, err := engine.New(engine.WithSchema(sortIdentitySchema(sortIdentityFixtures()[0])), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const source = `labels:items:map(p=>"\u0000FIRST"):sort:json="\u0000SECOND"`
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Span == nil || detail.Span.Start != strings.Index(source, `"\u0000FIRST"`) {
		t.Fatal("receiver diagnostic order", err)
	}
}
