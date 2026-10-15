package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func nullArgumentEngine(t *testing.T, methods ...hff.MethodDeclaration) *engine.Engine {
	t.Helper()
	caps := capabilities.Standard()
	for _, d := range methods {
		caps = caps.Allow(capabilities.Method(d.ID()))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(schema.Optional(schema.Field("nil", schema.NullType)), schema.String("name"))))), schema.Int("age"))), engine.WithCapabilities(caps), engine.WithMethods(methods...))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func nullArgumentSources() []string {
	sources := []string{
		`age>3 (labels.nil+(age+7)):type="null" age>2`,
		`(labels.nil+(age:round+7)):type="null" age>2`,
		`(labels.nil+(labels.name:len+7)):type="null" age>2`,
	}
	for _, op := range []string{"+", "-", "*", "/", "%"} {
		for _, left := range []string{"labels.nil", "labels.nil!", "labels:items:foldl(null,(a,p)=>a!)"} {
			sources = append(sources, "("+left+op+`7):type="null" age>2`)
		}
		for _, left := range []string{"7", `"7":number`, `"bad":number`} {
			for _, right := range []string{"labels.nil", "labels.nil!", "labels:items:foldl(null,(a,p)=>null)"} {
				sources = append(sources, "("+left+op+right+`):type="null" age>2`)
			}
		}
		sources = append(sources, `labels:items:map(p=>7`+op+`null):size>0`, `labels:items:map(p=>null!`+op+`7):size>0`)
		for _, left := range []string{`(7+0)`, `7:round`, `7:ceil:floor:round`, `"1s":duration:nanoseconds`} {
			sources = append(sources, "("+left+op+`labels.nil):type="null" age>2`)
		}
	}
	for _, op := range []string{"=", "!=", "<", "<=", ">", ">="} {
		for _, left := range []string{"7", `"7":number`, `(7+0)`, `7:round`, `"1s":duration:nanoseconds`} {
			sources = append(sources, `labels:items:map(p=>`+left+op+`labels.nil):size>0`)
		}
	}
	return sources
}

func nullArgumentOptions(format hffsql.JSONFormat) []hffsql.Option {
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format), hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")))}
}

func TestNullBinaryArgumentsRemainReferenced(t *testing.T) {
	e := nullArgumentEngine(t)
	for _, source := range nullArgumentSources() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, nullArgumentOptions(format)...)
			if err != nil {
				t.Fatal(source, format, err)
			}
			requireConcatArgsUsed(t, q)
			if q.Args()[len(q.Args())-1] != int64(2) && strings.Contains(source, "age>2") {
				t.Fatal("later sibling argument moved", source, q.Args())
			}
		}
	}
}

func TestNullBinaryArgumentsKeepExtensionValidation(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.null.touch", Name: "touch", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := nullArgumentEngine(t, d)
	for _, source := range []string{`(labels.nil+7:touch):type="null" age>2`, `(7:touch+labels.nil):type="null" age>2`, `(labels.nil!+7:touch):exists`, `(7+labels.nil):type="null" age>2:touch`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		calls := 0
		options := append(nullArgumentOptions(hffsql.JSON), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return c.Expression(schema.IntType, "{}", receiver)
		})))
		q, err := hffsql.Build(context.Background(), p, options...)
		if err != nil || calls != 1 {
			t.Fatal(source, calls, err)
		}
		requireConcatArgsUsed(t, q)
		if strings.HasPrefix(source, "(labels.nil") {
			q, err = hffsql.Build(context.Background(), p, nullArgumentOptions(hffsql.JSON)...)
			requireCode(t, err, hff.BackendError)
			if q != nil {
				t.Fatal("discarded operand skipped extension validation")
			}
		}
	}
}

func TestNullBinaryArgumentsKeepDiagnosticOrder(t *testing.T) {
	e := nullArgumentEngine(t)
	for _, source := range []string{
		`(labels.nil+("\u0000":number)):exists`,
		`("\u0000":number+labels.nil):exists`,
		`(labels.nil+1):exists labels.name="\u0000"`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, nullArgumentOptions(hffsql.JSON)...)
		requireCode(t, err, hff.BackendError)
		var diagnostic *hff.Error
		if q != nil || !errors.As(err, &diagnostic) || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("source diagnostic was lost or moved", source, err)
		}
	}
}

func TestNullBinaryArgumentsCancellation(t *testing.T) {
	e := nullArgumentEngine(t)
	for _, source := range []string{`(labels.nil+7):type="null" age>2`, `(7+labels.nil):type="null" age>2`, `(7+labels:items:foldl(null,(a,p)=>null)):type="null" age>2`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, nullArgumentOptions(hffsql.JSON)...); err != nil {
			t.Fatal(err)
		}
		for after := int64(1); after <= baseline.checks.Load(); after++ {
			base, cancel := context.WithCancel(context.Background())
			ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
			q, err := hffsql.Build(ctx, p, nullArgumentOptions(hffsql.JSON)...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatal("partial query escaped cancellation", source, after, q, err)
			}
		}
	}
}
