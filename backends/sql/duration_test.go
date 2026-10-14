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

func TestComputedDurationRepresentationBoundaries(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:duration:in(data:fromjson)`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("data"))))
		detail := requireCode(t, err, hff.BackendError)
		if detail.Span == nil {
			t.Fatalf("%s: representation rejection lacks source span", source)
		}
	}
	for _, source := range []string{`data:duration:in(data:fromjson:[string|int]):exists`, `[data:duration]:size=1`, `{"d":data:duration}:size=1`, `[data:duration]:json="[]"`, `data:fromjson:[list]:map(x=>x:duration):size=1`, `[data:duration:nanoseconds]:size=1`, `[data:duration:string]:size=1`, `data:duration:[duration]:exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("data")))); err != nil {
			t.Fatal(source, err)
		}
	}
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	foundDurationDetail := false
	for _, detail := range profile.Details() {
		if detail.Name == "duration" {
			foundDurationDetail = true
			if !strings.Contains(detail.Value, "schema-known JSON/JSONB duration strings") ||
				!strings.Contains(detail.Value, "native bigint nanosecond duration columns") {
				t.Fatalf("duration profile reports stale storage support: %s", detail.Value)
			}
			break
		}
	}
	if !foundDurationDetail {
		t.Fatal("duration profile detail missing")
	}
	for _, id := range []string{"string.duration", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds"} {
		feature, ok := profile.Feature(capabilities.Method(id))
		if !ok || feature.Level != hff.Conditional {
			t.Fatalf("computed duration profile missing %s", id)
		}
	}
}

func TestNativeDurationRelationCallbacksBuild(t *testing.T) {
	ctx := context.Background()
	itemType := schema.NullableType(schema.DurationType)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", itemType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("stored_durations"), "item"),
		hffsql.WithCorrelation(hffsql.Boolean(true)),
		hffsql.WithRelationItem(itemType, hffsql.Bind(nil, hffsql.Column("item", "elapsed_ns"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`items:any(x=>x:nanoseconds=1)`,
		`items:any(x=>x=null)`,
		`items:all(x=>x:exists)`,
		`items:size=2`,
		`items:has(-1)`,
		`items:has(1.5)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(query.Where(), "json_agg(") {
			t.Errorf("%s: Duration relation was materialized: %s", source, query.Where())
		}
		if strings.Contains(source, "=>") && !strings.Contains(query.Where(), `"item"."elapsed_ns"`) {
			t.Errorf("%s: direct Duration item was not read: %s", source, query.Where())
		}
		if strings.Contains(source, ":size") || source == `items:has(1.5)` {
			if !strings.Contains(query.Where(), "count(*)") {
				t.Errorf("%s: relation cardinality was not counted: %s", source, query.Where())
			}
		}
		if source == `items:has(-1)` && (!strings.Contains(query.Where(), "EXISTS (SELECT 1") || strings.Contains(query.Where(), "count(*)")) {
			t.Errorf("%s: end index was not lowered to existence: %s", source, query.Where())
		}
	}
}

func TestNativeDurationRelationSizeAndHasKeepNullState(t *testing.T) {
	ctx := context.Background()
	itemType := schema.NullableType(schema.DurationType)
	profileType := schema.Struct(schema.Nullable(schema.List("items", itemType)))
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Field("profile", profileType)),
		schema.Nullable(schema.Float("index")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("duration_items"), "item"),
		hffsql.WithCorrelation(hffsql.Boolean(true)),
		hffsql.WithRelationState(
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "items_present"), schema.BoolType)),
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "items_null"), schema.BoolType)),
		),
		hffsql.WithRelationItem(itemType, hffsql.Bind(nil, hffsql.Column("item", "elapsed_ns"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.BindStruct(hff.Path{"profile"},
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_present"), schema.BoolType)),
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_null"), schema.BoolType)),
		hffsql.BindRelation(hff.Path{"items"}, relation),
	)
	for _, source := range []string{`profile.items:size=0`, `profile.items:has(index)`, `profile.items:has(null!)`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			binding, hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "index")),
		))
		if err != nil {
			t.Fatal(source, err)
		}
		expected := []string{"profile_present", "profile_null", "items_present", "items_null"}
		if source == `profile.items:size=0` {
			expected = append(expected, "NOT EXISTS (SELECT 1")
			if strings.Contains(query.Where(), "count(*)") {
				t.Errorf("%s counted rows for emptiness: %s", source, query.Where())
			}
		} else {
			expected = append(expected, "count(*)")
		}
		for _, token := range expected {
			if !strings.Contains(query.Where(), token) {
				t.Errorf("%s lost %s: %s", source, token, query.Where())
			}
		}
		if strings.Contains(query.Where(), "json_agg(") {
			t.Errorf("%s materialized Duration items: %s", source, query.Where())
		}
		if source == `profile.items:has(index)` && !strings.Contains(query.Where(), "::bigint") {
			t.Errorf("%s lost exact int64 boundary check: %s", source, query.Where())
		}
	}
}

func TestStoredDurationBindingFormats(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		typ  hff.Type
		want bool
	}{
		{schema.DurationType, true},
		{schema.Union(schema.DurationType, schema.NullType), true},
		{schema.Union(schema.DurationType, schema.IntType), true},
		{schema.Union(schema.DurationType, schema.StringType, schema.NullType), false},
		{schema.Union(schema.DurationType, schema.BytesType), false},
		{schema.Union(schema.ListOf(schema.DurationType), schema.ListOf(schema.AnyType)), false},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", test.typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `data:type="duration"`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{"", hffsql.JSON, hffsql.JSONB} {
			binding := hffsql.Bind(hff.Path{"data"}, hffsql.Column("data"))
			if format != "" {
				binding = hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), format)
			}
			_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			supported := (format == "" && (test.typ.Equal(schema.DurationType) ||
				test.typ.Equal(schema.Union(schema.DurationType, schema.NullType)))) ||
				(format != "" && test.want)
			if supported {
				if err != nil {
					t.Errorf("%s %s: rejected schema-known Duration: %v", test.typ.Key(), format, err)
				}
			} else {
				requireCode(t, err, hff.BackendError)
			}
		}
	}
}

func TestNativeDurationMethodsAndPredicatesBuild(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Duration("elapsed")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.Bind(hff.Path{"elapsed"}, hffsql.Column("records", "elapsed"))
	for _, source := range []string{
		`elapsed`, `elapsed=null`, `elapsed:exists`, `elapsed>"1ns":duration`,
		`elapsed:seconds>=60`, `elapsed:nanoseconds=1`, `elapsed:string="1ns"`,
		`elapsed:json="\"1ns\""`, `elapsed:in("1ns":duration,null)`,
		`elapsed:[duration]:exists`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err != nil {
			t.Error(source, err)
		}
	}
}

func TestOmittedFlattenedDurationFieldIsNull(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("profile", schema.Struct(
		schema.Optional(schema.Nullable(schema.Duration("elapsed"))),
	)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `profile.elapsed:type="null"`)
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false))
	if _, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err != nil {
		t.Fatal(err)
	}
}

func TestFlattenedNativeDurationFieldBuilds(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("profile", schema.Struct(
		schema.Nullable(schema.Duration("elapsed")),
	)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `profile.elapsed:nanoseconds=1`)
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false),
		hffsql.Bind(hff.Path{"elapsed"}, hffsql.Column("records", "elapsed_ns")))
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
	if err != nil {
		t.Fatal(err)
	}
	if query.Where() == "" {
		t.Fatal("empty Duration predicate")
	}
}
