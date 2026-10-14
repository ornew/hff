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

func TestTimestampInFlattenedStruct(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Field("profile", schema.Struct(schema.Timestamp("created")))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.BindStruct(hff.Path{"profile"},
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_present"), schema.BoolType)),
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_null"), schema.BoolType)),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("records", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("records", "created_ns_remainder"))))
	for _, source := range []string{
		`profile.created:after("2026-10-01T00:00:00Z":timestamp)`,
		`profile.created:year=2026`,
		`profile.created=null`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), `"records"."created_ns_remainder"::numeric`) {
			t.Error(source, "lost the stored nanosecond remainder:", query.Where())
		}
		if !strings.Contains(query.Where(), `"records"."profile_present"`) ||
			!strings.Contains(query.Where(), `"records"."profile_null"`) {
			t.Error(source, "lost the nullable parent state:", query.Where())
		}
	}
	program, err := e.Compile(ctx, `profile:exists`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
	requireCode(t, err, hff.BackendError)
	for _, source := range []string{
		`[profile.created]:size=1`,
		`{"t":profile.created}:size=1`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), `"records"."profile_present"`) ||
			!strings.Contains(query.Where(), `"records"."profile_null"`) {
			t.Error(source, "lost the nullable parent state:", query.Where())
		}
	}
}

func TestTimestampInRelationPredicates(t *testing.T) {
	ctx := context.Background()
	item := schema.Struct(schema.Timestamp("created"))
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.List("events", item),
		schema.List("times", schema.TimestampType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := make([]hffsql.Binding, 0, 2)
	for _, setting := range []struct {
		path string
		typ  hff.Type
		item hffsql.Binding
	}{
		{"events", item, hffsql.BindStruct(nil, hffsql.Boolean(true), hffsql.Boolean(false),
			hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("event", "created_at"),
				hffsql.WithNanosecondRemainder(hffsql.Column("event", "created_ns_remainder"))))},
		{"times", schema.TimestampType, hffsql.BindTimestamp(nil, hffsql.Column("event", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("event", "created_ns_remainder")))},
	} {
		relation, err := hffsql.NewRelation(
			hffsql.WithRelationSource(hffsql.Column("stored_events"), "event"),
			hffsql.WithCorrelation(hffsql.Boolean(true)),
			hffsql.WithRelationItem(setting.typ, setting.item),
		)
		if err != nil {
			t.Fatal(err)
		}
		bindings = append(bindings, hffsql.BindRelation(hff.Path{setting.path}, relation))
	}
	for _, source := range []string{
		`events:any(event=>event.created:after("2026-10-01T00:00:00Z":timestamp))`,
		`times:any(stamp=>stamp:after("2026-10-01T00:00:00Z":timestamp))`,
		`times:any(stamp=>stamp:[timestamp]:year=0)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if source == `times:any(stamp=>stamp:after("2026-10-01T00:00:00Z":timestamp))` {
			if strings.Contains(query.Where(), "EXTRACT(EPOCH") || !strings.Contains(query.Where(), `"event"."created_ns_remainder"`) {
				t.Error(source, "lost the direct nanosecond comparison:", query.Where())
			}
		} else if !strings.Contains(query.Where(), `"event"."created_ns_remainder"::numeric`) {
			t.Error(source, "lost the stored nanosecond remainder:", query.Where())
		}
	}
	program, err := e.Compile(ctx, `times:any(stamp=>stamp:[int]:exists)`)
	if err != nil {
		t.Fatal("compile disjoint relation type guard:", err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
	if err != nil {
		t.Fatal("build disjoint relation type guard:", err)
	}
	if !strings.Contains(query.Where(), "NULL::bigint") {
		t.Fatal("disjoint relation type guard did not preserve its result type:", query.Where())
	}
	for _, source := range []string{
		`events:size=2`, `times:size=2`,
		`events:has(0)`, `times:has(-1)`, `times:has(1.5)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(query.Where(), "json_agg(") {
			t.Error(source, "relation index check materialized Timestamp items:", query.Where())
		}
		if strings.Contains(source, ":has(0)") || strings.Contains(source, ":has(-1)") {
			if !strings.Contains(query.Where(), "EXISTS (SELECT 1") || strings.Contains(query.Where(), "count(*)") {
				t.Error(source, "end index was not lowered to existence:", query.Where())
			}
		} else if !strings.Contains(query.Where(), "count(*)") {
			t.Error(source, "relation cardinality was not counted:", query.Where())
		}
	}
}

func TestNestedTimestampBindingValidation(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("profile", schema.Struct(schema.Timestamp("created"))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `profile.created:exists`)
	if err != nil {
		t.Fatal(err)
	}
	for _, child := range []hffsql.Binding{
		hffsql.Bind(hff.Path{"created"}, hffsql.Column("created_at")),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("created_at"))),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("created_at"),
			hffsql.TimestampBindingOption{}),
	} {
		binding := hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false), child)
		_, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		requireCode(t, err, hff.ConfigurationError)
	}
}

func TestTimestampRelationItemRequiresNativeBinding(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("times", schema.TimestampType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `times:any(stamp=>stamp:exists)`)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range []hffsql.Binding{
		hffsql.Bind(nil, hffsql.Column("event", "created_at")),
		hffsql.BindTimestamp(nil, hffsql.Column("event", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("event", "created_at"))),
		hffsql.BindTimestamp(nil, hffsql.Column("event", "created_at"),
			hffsql.TimestampBindingOption{}),
	} {
		relation, err := hffsql.NewRelation(
			hffsql.WithRelationSource(hffsql.Column("stored_events"), "event"),
			hffsql.WithCorrelation(hffsql.Boolean(true)),
			hffsql.WithRelationItem(schema.TimestampType, item),
		)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindRelation(hff.Path{"times"}, relation)))
		requireCode(t, err, hff.ConfigurationError)
	}
}

func TestTimestampRelationSizeKeepsParentAndListState(t *testing.T) {
	ctx := context.Background()
	profileType := schema.Struct(schema.List("times", schema.TimestampType))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("profile", profileType)))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("stored_times"), "item"),
		hffsql.WithCorrelation(hffsql.Boolean(true)),
		hffsql.WithRelationState(
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "times_present"), schema.BoolType)),
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "times_null"), schema.BoolType)),
		),
		hffsql.WithRelationItem(schema.TimestampType,
			hffsql.BindTimestamp(nil, hffsql.Column("item", "created_at"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.BindStruct(hff.Path{"profile"},
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_present"), schema.BoolType)),
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_null"), schema.BoolType)),
		hffsql.BindRelation(hff.Path{"times"}, relation),
	)
	for _, source := range []string{`profile.times:size=0`, `profile.times:has(-1)`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if err != nil {
			t.Fatal(source, err)
		}
		expected := []string{"profile_present", "profile_null", "times_present", "times_null"}
		if source == `profile.times:has(-1)` {
			expected = append(expected, "EXISTS (SELECT 1")
			if strings.Contains(query.Where(), "count(*)") {
				t.Errorf("%s counted rows for existence: %s", source, query.Where())
			}
		} else {
			expected = append(expected, "NOT EXISTS (SELECT 1")
			if strings.Contains(query.Where(), "count(*)") {
				t.Errorf("%s counted rows for emptiness: %s", source, query.Where())
			}
		}
		for _, name := range expected {
			if !strings.Contains(query.Where(), name) {
				t.Errorf("%s lost %s: %s", source, name, query.Where())
			}
		}
	}
}

func TestRelationHasDynamicIndexKeepsJSONLowering(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.List("items", schema.IntType),
		schema.Field("index", schema.AnyType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:has(index)`)
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("stored_items"), "item"),
		hffsql.WithCorrelation(hffsql.Boolean(true)),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(nil, hffsql.Column("item", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindRelation(hff.Path{"items"}, relation),
		hffsql.BindJSON(hff.Path{"index"}, hffsql.Column("records", "index_json"), hffsql.JSON),
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where(), "json_agg(") {
		t.Error("dynamic index lost JSON family dispatch:", query.Where())
	}
}

func TestRelationHasFloatIndexComparesBigintExactly(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.List("times", schema.TimestampType),
		schema.Nullable(schema.Float("index")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("stored_times"), "item"),
		hffsql.WithCorrelation(hffsql.Boolean(true)),
		hffsql.WithRelationItem(schema.TimestampType,
			hffsql.BindTimestamp(nil, hffsql.Column("item", "created_at"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`times:has(index)`, `times:has(null!)`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindRelation(hff.Path{"times"}, relation),
			hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "index")),
		))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), "count(*)") {
			t.Error(source, "relation cardinality lost:", query.Where())
		}
		if source == `times:has(index)` && !strings.Contains(query.Where(), "::bigint") {
			t.Error(source, "float index and bigint cardinality lost exact comparison:", query.Where())
		}
	}
}
