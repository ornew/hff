package sql_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func literalThresholdEngine(t testing.TB) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Int("i")), schema.Nullable(schema.Float("f")), schema.String("name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func literalThresholdOptions() []hffsql.Option {
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.Bind(hff.Path{"i"}, hffsql.Column("records", "i")),
		hffsql.Bind(hff.Path{"f"}, hffsql.Column("records", "f")),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
	)}
}

func TestDirectMixedLiteralThresholdParameters(t *testing.T) {
	e := literalThresholdEngine(t)
	for _, tc := range []struct {
		source string
		args   []any
	}{
		{`i>18.5`, []any{int64(19)}}, {`i<-18.5`, []any{int64(-19)}},
		{`i>=5e-324`, []any{int64(1)}}, {`i<=-5e-324`, []any{int64(-1)}},
		{`i=-0.0`, []any{int64(0)}}, {`i=1.5`, nil}, {`i!=1.5`, nil},
		{`i<1.7976931348623157e308`, nil}, {`i>=9223372036854775808.0`, nil},
		{`f=18`, []any{float64(18)}}, {`f=9007199254740993`, nil},
		{`f<9007199254740993`, []any{float64(9007199254740992)}},
		{`f>9007199254740993`, []any{float64(9007199254740994)}},
		{`f>=9223372036854775807`, []any{float64(9223372036854775808)}},
		{`i>18.5 name="tail" f>"9007199254740993":number`, []any{int64(19), "tail", float64(9007199254740994)}},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(context.Background(), p, literalThresholdOptions()...)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if strings.Contains(q.Where(), "SELECT") || strings.Contains(q.Where(), "floor(") {
			t.Fatal("literal threshold retained runtime comparison kernels", tc.source, q.Where())
		}
		if !reflect.DeepEqual(q.Args(), tc.args) {
			t.Errorf("%s args=%#v want%#v", tc.source, q.Args(), tc.args)
		}
	}
}

func TestMixedLiteralThresholdsKeepValueAndGuardFallback(t *testing.T) {
	e := literalThresholdEngine(t)
	for _, source := range []string{`i!>18.5`, `f!>18`, `i>f`, `(i>18.5):type="bool"`, `[i>18.5][0]`, `[18.5<i][0]`, `i>"bad":number`, `i>"18.5":number`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, literalThresholdOptions()...)
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "SELECT") {
			t.Fatal("ineligible value/guard context bypassed ordinary lowering", source, q.Where())
		}
	}
}

func TestMixedLiteralThresholdsPreserveJSONAndConversionChecks(t *testing.T) {
	e := literalThresholdEngine(t)
	p, err := e.Compile(context.Background(), `i>18.5`)
	if err != nil {
		t.Fatal(err)
	}
	options := literalThresholdOptions()
	options = append(options, hffsql.WithBindings(hffsql.BindJSON(hff.Path{"i"}, hffsql.Column("records", "i"), hffsql.JSON)))
	q, err := hffsql.Build(context.Background(), p, options...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), "floor(") {
		t.Fatal("JSON binding bypassed its ordinary representation lowering", q.Where())
	}
	p, err = e.Compile(context.Background(), `f>"\u0000":number`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, literalThresholdOptions()...)
	requireCode(t, err, hff.BackendError)
}
