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

func TestTypedNumericResultAvoidsJSONRoundTrip(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{
		`i+2>0`, `i%2>0`, `i/2>0`, `f+2.0>0`, `f-2.0>0`, `f*2.0>0`, `f/2.0>0`, `i+f>0`,
		`(-i)>0`, `(-f)>0`, `i:round>0`, `f:round>0`, `f:ceil>0`, `f:floor>0`,
	} {
		q := numericPruningQuery(t, e, source)
		if strings.Contains(q.Where(), "to_json(") || strings.Contains(q.Where(), "::json") {
			t.Errorf("%s retained a computed-result JSON round trip: %s", source, q.Where())
		}
	}
}

func TestTypedNumericResultRetainsUnionDispatch(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{`both+2>0`, `data+2>0`, `(-both)>0`, `both:round>0`} {
		q := numericPruningQuery(t, e, source)
		if !strings.Contains(q.Where(), "to_json(") || !strings.Contains(q.Where(), "::json") {
			t.Errorf("%s lost numeric-union representation: %s", source, q.Where())
		}
	}
}

func TestTypedNumericResultRetainsFloatLossRejection(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{`(f+2.0):json="0.0"`, `(-f):json="0.0"`, `f:round:json="0.0"`, `floats:map(x=>x+2.0):json="[]"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSONB, false)...)
		requireCode(t, err, hff.BackendError)
		if _, err = hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSONB, true)...); err != nil {
			t.Fatal(source, err)
		}
	}
}

func TestTypedNumericResultKeepsNullOnlyCarrier(t *testing.T) {
	_, d := foldedNullExtension(t)
	base := numericPruningEngine(t)
	e, err := engine.New(engine.WithSchema(base.Schema()), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `name:nullcarrier(data:[null]+guard)`)
	if err != nil {
		t.Fatal(err)
	}
	options := numericPruningOptions(hffsql.JSON, false)
	options = append(options, hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
		return c.Expression(schema.BoolType, "pg_typeof({})::text='unknown'", args[0])
	})))
	q, err := hffsql.Build(context.Background(), p, options...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), "pg_typeof(NULL)") {
		t.Fatal("null-only result changed its untyped carrier", q.Where())
	}
}
