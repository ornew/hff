package sql_test

import (
	"context"
	"runtime"
	"testing"
	"unicode"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestBackendProfilesDiscloseFloatModels(t *testing.T) {
	postgres, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.FloatType))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `items:sum>0 | items:avg>0`)
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("items"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []hff.Profile{local.Profile(), filter.Profile(), postgres, query.Profile()} {
		details := make(map[string]string)
		for _, detail := range profile.Details() {
			details[detail.Name] = detail.Value
		}
		for _, name := range []string{"float.model", "float.parsing", "float.arithmetic", "float.rounding", "float.aggregation", "float.format"} {
			if details[name] == "" {
				t.Errorf("%s omitted %s", profile.Backend(), name)
			}
		}
		for _, method := range []string{"list.sum", "list.avg"} {
			if support, known := profile.Feature(capabilities.Method(method)); !known || support.Level == hff.UnknownSupport || support.Level == hff.Unsupported {
				t.Errorf("%s omitted applicable support for %s", profile.Backend(), method)
			}
		}
	}
}

func TestSQLProfilesDoNotInventDatabaseGuarantees(t *testing.T) {
	base, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	if !base.Valid() || len(base.Versions()) != 2 || len(base.Guarantees()) != 0 || base.Capabilities().Specified() {
		t.Fatal("static translation profile fabricated database state or policy")
	}
	if version := base.Versions()[1]; version.Component != "unicode" || version.Version != unicode.Version {
		t.Fatal("translator Unicode compatibility version missing")
	}
	if version := base.Versions()[0]; version.Component != "regex.parser" || version.Version != "Go regexp "+runtime.Version() {
		t.Fatal("translator regex parser version missing")
	}
	for _, id := range capabilities.Standard().IDs() {
		if _, known := base.Feature(id); !known {
			t.Fatalf("missing SQL support entry: %s", id)
		}
	}
	if support, _ := base.Feature(capabilities.Method("timestamp.year")); support.Level != hff.Conditional {
		t.Fatalf("UTC timestamp extraction is not advertised as conditional: %v", support)
	}
	for _, method := range []string{"list.count", "list.filter"} {
		if support, known := base.Feature(capabilities.Method(method)); !known || support.Level != hff.Conditional {
			t.Fatalf("%s translation support is not advertised as conditional: %v", method, support)
		}
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `name="Alice"`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("name"))))
	if err != nil {
		t.Fatal(err)
	}
	profile := q.Profile()
	if !profile.Capabilities().Has(capabilities.Comparison) || profile.Backend() != "sql/postgresql" {
		t.Fatal("built query profile lost its policy or identity")
	}
	if compile, known := profile.CompileLimits(); !known || compile.MaxQueryBytes != hff.DefaultLimits().MaxQueryBytes {
		t.Fatal("compile limits missing")
	}
	if _, known := profile.RuntimeLimits(); known || profile.HasGuarantee(hff.StepBudgetGuarantee) || profile.HasGuarantee(hff.EvaluationOrderGuarantee) {
		t.Fatal("query claims budgets/order it cannot enforce")
	}
	if _, known := profile.InputLimits(); known {
		t.Fatal("query claims to validate the database rows")
	}
	if _, err := hffsql.Profile("unknown"); err == nil {
		t.Fatal("unknown dialect accepted")
	}
	var nilQuery *hffsql.Query
	if nilQuery.Profile().Valid() {
		t.Fatal("nil query fabricated profile")
	}
}

func TestSQLProfileReportsRegisteredExtensions(t *testing.T) {
	ds := extensionDeclarations(t)
	e := extensionEngine(t, ds)
	p, err := e.Compile(context.Background(), `name:customprefix("a")`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, extensionOptions(ds)...)
	if err != nil {
		t.Fatal(err)
	}
	profile := q.Profile()
	if len(profile.Methods()) != len(ds) {
		t.Fatal("configured SQL declarations missing")
	}
	for _, d := range ds {
		if support, known := profile.Feature(capabilities.Method(d.ID())); !known || support.Level != hff.Conditional {
			t.Fatalf("extension support missing for %s", d.ID())
		}
	}
}
