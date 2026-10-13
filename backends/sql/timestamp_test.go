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

func timestampEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Timestamp("created")))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestTimestampBindingPreservesNanoseconds(t *testing.T) {
	ctx := context.Background()
	e := timestampEngine(t)
	binding := hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("events", "created_at"),
		hffsql.WithNanosecondRemainder(hffsql.Column("events", "created_ns_remainder")))
	for _, source := range []string{
		`created:after("2026-10-01T00:00:00Z":timestamp)`,
		`created>"2026-10-01T00:00:00.000000123Z":timestamp`,
		`created:before("0000-01-01T00:00:00Z":timestamp)`,
		`created:add("1ns":duration):year=2026`,
		`created:year=0`,
		`created:milliseconds=123`,
		`created:string="2026-10-01T00:00:00.000000123Z"`,
		`created:json="\"2026-10-01T00:00:00.000000123Z\""`,
		`created:in("0000-01-01T00:00:00Z":timestamp)`,
		`created:type="timestamp"`,
		`created:exists`,
		`created:[timestamp]:exists`,
		`created:[timestamp|null]:type="null"`,
		`created=null`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatalf("%s: compile: %v", source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if err != nil {
			t.Errorf("%s: build: %v", source, err)
			continue
		}
		if !strings.Contains(query.Where(), `EXTRACT(EPOCH FROM "events"."created_at")`) ||
			!strings.Contains(query.Where(), `"events"."created_ns_remainder"::numeric`) {
			t.Errorf("%s: timestamp precision was not preserved: %s", source, query.Where())
		}
	}
	program, err := e.Compile(ctx, `created="0000-01-01T00:00:00Z":timestamp`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
	if err != nil {
		t.Fatal(err)
	}
	args := query.Args()
	if len(args) != 1 || args[0] != "-62167219200000000000" {
		t.Fatalf("year 0000 changed its instant: %v", args)
	}
	program, err = e.Compile(ctx, `created:[int]:exists`)
	if err != nil {
		t.Fatal("compile disjoint type guard:", err)
	}
	query, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
	if err != nil {
		t.Fatal("build disjoint type guard:", err)
	}
	if !strings.Contains(query.Where(), "NULL::bigint") {
		t.Fatal("disjoint type guard did not preserve its result type:", query.Where())
	}
}

func TestTimestampBindingRequiresExplicitStorageContract(t *testing.T) {
	ctx := context.Background()
	e := timestampEngine(t)
	program, err := e.Compile(ctx, `created:exists`)
	if err != nil {
		t.Fatal(err)
	}
	options := func(binding hffsql.Binding) []hffsql.Option {
		return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)}
	}
	for _, binding := range []hffsql.Binding{
		hffsql.Bind(hff.Path{"created"}, hffsql.Column("created_at")),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("created_at"))),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("created_at"),
			hffsql.TimestampBindingOption{}),
	} {
		_, err := hffsql.Build(ctx, program, options(binding)...)
		requireCode(t, err, hff.ConfigurationError)
	}
	if _, err := hffsql.Build(ctx, program, options(hffsql.BindTimestamp(
		hff.Path{"created"}, hffsql.Column("created_at")))...); err != nil {
		t.Fatal("microsecond-exact Timestamp binding rejected:", err)
	}
}

func TestTimestampMethodBoundaries(t *testing.T) {
	ctx := context.Background()
	e := timestampEngine(t)
	binding := hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("created_at"))
	for _, source := range []string{
		`created:year("Asia/Tokyo")=2026`,
		`created:string("Asia/Tokyo")="2026-10-01T09:00:00+09:00"`,
		`created:after(created)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if source == `created:after(created)` {
			if err != nil {
				t.Error(source, err)
			}
		} else {
			requireCode(t, err, hff.BackendError)
		}
	}
}

func TestTimestampNullConversionsPreserveRepresentation(t *testing.T) {
	ctx := context.Background()
	e := timestampEngine(t)
	binding := hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("created_at"))
	for _, source := range []string{
		`created:json=[null:timestamp]:json`,
		`created:type=(null:timestamp):type`,
		`created=(null:timestamp)`,
		`created=(null!):timestamp`,
		`created=(null:timestamp:year)`,
		`created=(null:timestamp:before(created))`,
		`created=(null:timestamp:add("1ns":duration))`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err != nil {
			t.Errorf("%s: rejected null Timestamp conversion: %v", source, err)
		}
	}
}

func TestTimestampRemainderAliasDoesNotShadowRelationCapture(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Timestamp("created"), schema.List("items", schema.IntType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:any(item=>item=1 created:after("2026-10-01T00:00:00Z":timestamp))`)
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("hff_items"), "r"),
		hffsql.WithCorrelation(hffsql.TrustedSQL(`r.parent_id=events.id`,
			hffsql.Column("r", "parent_id"), hffsql.Column("events", "id"))),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(hff.Path{}, hffsql.Column("r", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("events", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("r", "created_ns_remainder"))),
		hffsql.BindRelation(hff.Path{"items"}, relation),
	))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Where(), `SELECT 1 FROM "hff_items" AS "r"`) ||
		!strings.Contains(query.Where(), `json_array_elements`) {
		t.Fatal("relation alias captured the outer Timestamp remainder:", query.Where())
	}
}
