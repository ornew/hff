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

func flattenedSchema() hff.Type {
	return schema.Struct(schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(
		schema.Nullable(schema.String("name")), schema.Nullable(schema.Int("age")),
		schema.Optional(schema.Nullable(schema.Field("child", schema.Struct(schema.Nullable(schema.String("name")))))),
		schema.Optional(schema.String("ignored")),
		schema.Nullable(schema.List("scores", schema.NullableType(schema.IntType))),
		schema.Nullable(schema.Float("rate")), schema.Nullable(schema.Map("labels", schema.StringType)),
	)))))
}

func flattenedEngine(t testing.TB) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(flattenedSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func flattenedBinding() hffsql.Binding {
	return hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("records.profile_present", hffsql.Column("records")),
		hffsql.TrustedSQL("records.profile_null"),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "profile_name")),
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "profile_age")),
		hffsql.BindStruct(hff.Path{"child"}, hffsql.TrustedSQL("records.child_present"), hffsql.TrustedSQL("records.child_null"),
			hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "child_name"))),
		hffsql.Bind(hff.Path{"scores"}, hffsql.Column("records", "scores")),
		hffsql.Bind(hff.Path{"rate"}, hffsql.Column("records", "rate")),
		hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))
}

func flattenedQueries() []string {
	return []string{`profile`, `profile=null`, `profile!=null`, `profile:exists`, `profile:has("name")`,
		`profile:has("ignored")`, `profile.name=null`, `profile!.name=null`, `!profile!.name=null`,
		`profile.name="Alice"`, `profile.age>=18`, `profile:has("child")`, `profile.child=null`,
		`profile!.child!.name=null`, `!profile!.child!.name=null`, `profile.child={"name":null}`,
		`profile.child={}`, `profile:size=5`, `profile:size=6`, `profile.scores[-1]=null`,
		`profile.scores:any(x=>x>10)`, `profile.scores=[10,20,null]`, `profile.rate:tostring="-0.0"`,
		`profile.labels.app="auth"`, `profile:tojson:contains("child")`, `profile=profile`,
		`profile:keys:contains("name","age")`, `profile:get("child")=null`}
}

func TestFlattenedStructureBuildsWithExplicitParentStates(t *testing.T) {
	e := flattenedEngine(t)
	for _, source := range flattenedQueries() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(flattenedBinding()))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "records.profile_present") || !strings.Contains(q.Where(), "records.profile_null") {
			t.Fatal("query omitted explicit parent checks")
		}
	}
}

func TestFlattenedStructureRejectsInvalidMappings(t *testing.T) {
	e := flattenedEngine(t)
	p, err := e.Compile(context.Background(), `profile:exists`)
	if err != nil {
		t.Fatal(err)
	}
	for _, binding := range []hffsql.Binding{
		hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false")),
		hffsql.BindStruct(hff.Path{"profile"}, hffsql.SQLPredicate{}, hffsql.TrustedSQL("false")),
		hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"), hffsql.Bind(hff.Path{"unknown"}, hffsql.Column("unknown"))),
		hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"), hffsql.Bind(hff.Path{"name", "nested"}, hffsql.Column("name"))),
		hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"), hffsql.Bind(hff.Path{"name"}, hffsql.Column("name")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("name2"))),
	} {
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err == nil {
			t.Fatal("invalid mapping accepted")
		}
	}
}

func TestFlattenedStructureTracksOnlyLossyFloatFields(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("profile", schema.Struct(schema.Float("rate"), schema.String("label"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `profile:tojson!=""`)
	if err != nil {
		t.Fatal(err)
	}
	for _, lossy := range []bool{false, true} {
		rate := hffsql.Bind(hff.Path{"rate"}, hffsql.Column("rate"))
		if lossy {
			rate = hffsql.BindJSON(hff.Path{"rate"}, hffsql.Column("rate"), hffsql.JSONB)
		}
		binding := hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("true"), hffsql.TrustedSQL("false"), rate, hffsql.BindJSON(hff.Path{"label"}, hffsql.Column("label"), hffsql.JSONB))
		_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if (err != nil) != lossy {
			t.Fatalf("lossy=%v: %v", lossy, err)
		}
	}
}

func TestFlattenedStructureSnapshotsCallerSlices(t *testing.T) {
	path := hff.Path{"profile"}
	references := []hffsql.ColumnRef{hffsql.Column("records")}
	fields := []hffsql.Binding{hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))}
	binding := hffsql.BindStruct(path, hffsql.TrustedSQL("true", references...), hffsql.TrustedSQL("false"), fields...)
	path[0] = "unknown"
	references[0] = hffsql.Column("")
	fields[0] = hffsql.Binding{}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("profile", schema.Struct(schema.String("name"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `profile.name="Alice"`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding)); err != nil {
		t.Fatal(err)
	}
}
