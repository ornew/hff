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

func numericPruningEngine(t testing.TB) *engine.Engine {
	t.Helper()
	i := schema.Union(schema.StringType, schema.IntType, schema.NullType)
	f := schema.Union(schema.StringType, schema.FloatType, schema.NullType)
	both := schema.Union(i, f)
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("i", i), schema.Field("f", f), schema.Field("both", both), schema.Any("data"),
		schema.List("items", i), schema.List("floats", f), schema.Nullable(schema.Int("guard")), schema.String("name"),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func TestNumericBranchPruningPreservesBuildCancellation(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{`i+2>0`, `f+2.0>0`, `(-i)>0`, `(-f)>0`, `i:round>0`, `f:round>0`, `items:any(x=>x+2>0)`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		options := numericPruningOptions(hffsql.JSON, false)
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, options...); err != nil {
			t.Fatal(source, err)
		}
		for after := int64(1); after <= baseline.checks.Load(); after++ {
			base, cancel := context.WithCancel(context.Background())
			ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
			q, err := hffsql.Build(ctx, p, options...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("%s checkpoint %d: query=%v error=%v", source, after, q, err)
			}
		}
	}
}

func numericPruningOptions(format hffsql.JSONFormat, metadata bool) []hffsql.Option {
	var bindings []hffsql.Binding
	for _, name := range []string{"i", "f", "both", "data", "items", "floats"} {
		var opts []hffsql.JSONBindingOption
		if metadata {
			opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
		}
		bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, opts...))
	}
	bindings = append(bindings, hffsql.Bind(hff.Path{"guard"}, hffsql.Column("records", "guard")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")))
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)}
}

func numericPruningQuery(t testing.TB, e *engine.Engine, source string) *hffsql.Query {
	t.Helper()
	p, err := e.Compile(context.Background(), source)
	if err != nil {
		t.Fatal(source, err)
	}
	q, err := hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSON, false)...)
	if err != nil {
		t.Fatal(source, err)
	}
	return q
}

func TestNumericBranchesPruneExcludedFamilies(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{`(i+2):type="int"`, `(i-2):type="int"`, `(i*2):type="int"`, `(i%2):type="int"`, `(-i):type="int"`, `i:round:type="int"`, `i:ceil:type="int"`, `i:floor:type="int"`, `(data:[string|int]+2):type="int"`} {
		q := numericPruningQuery(t, e, source)
		if strings.Contains(q.Where(), "double precision") || strings.Contains(q.Where(), "pg_input_is_valid") {
			t.Errorf("%s retained excluded Float lowering: %s", source, q.Where())
		}
	}
	for _, source := range []string{`(f+2.0):type="float"`, `(f-2.0):type="float"`, `(f*2.0):type="float"`, `(f/2.0):type="float"`, `(-f):type="float"`, `f:round:type="float"`, `f:ceil:type="float"`, `f:floor:type="float"`, `(both:[string|float]+f):type="float"`} {
		q := numericPruningQuery(t, e, source)
		if strings.Contains(q.Where(), "::numeric::bigint") || strings.Contains(q.Where(), "9223372036854775807") || strings.Contains(q.Where(), "-9223372036854775808") {
			t.Errorf("%s retained excluded Int lowering: %s", source, q.Where())
		}
	}
	q := numericPruningQuery(t, e, `(i/2):type="float"`)
	if !strings.Contains(q.Where(), "double precision / NULLIF") || strings.Contains(q.Where(), "1e300") {
		t.Fatal("Int division lost its Float result or retained unreachable Float-family arithmetic", q.Where())
	}
	q = numericPruningQuery(t, e, `(i+f):type="float"`)
	if !strings.Contains(q.Where(), "double precision") || strings.Contains(q.Where(), "9223372036854775807") {
		t.Fatal("mixed operation lost promotion or retained Int result", q.Where())
	}
}

func TestNumericBranchesKeepReachableFamiliesAndMetadataChecks(t *testing.T) {
	e := numericPruningEngine(t)
	for _, source := range []string{`(both+2):type="int"`, `(data+2):type="int"`} {
		q := numericPruningQuery(t, e, source)
		if !strings.Contains(q.Where(), "1e300") || !strings.Contains(q.Where(), "9223372036854775807") {
			t.Fatal(source, "lost a reachable numeric branch", q.Where())
		}
	}
	for _, source := range []string{`(data:[string|int]+2):type="int"`, `(both:[string|float]+2.0):type="float"`, `(-both:[string|float]):type="float"`, `data:[string|int]:round:type="int"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSONB, false)...)
		requireCode(t, err, hff.BackendError)
		if _, err = hffsql.Build(context.Background(), p, numericPruningOptions(hffsql.JSONB, true)...); err != nil {
			t.Fatal(source, err)
		}
	}
}

func TestNumericBranchPruningKeepsRightExtensionAndArgumentOrder(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.numeric.v1", Name: "numeric", Receiver: schema.StringType, Parameters: []hff.Type{schema.IntType}, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	base := numericPruningEngine(t)
	e, err := engine.New(engine.WithSchema(base.Schema()), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`i+name:numeric(7)>0 name="tail"`, `data:[null]+name:numeric(7)=null name="tail"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		calls := 0
		opts := numericPruningOptions(hffsql.JSON, false)
		opts = append(opts, hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return c.Expression(schema.IntType, "{}", args[0])
		})))
		q, err := hffsql.Build(context.Background(), p, opts...)
		if err != nil || calls != 1 {
			t.Fatalf("%s: calls=%d error=%v", source, calls, err)
		}
		args := q.Args()
		if len(args) < 2 || args[0] != int64(7) || args[len(args)-1] != "tail" {
			t.Fatalf("%s changed source argument order: %#v", source, args)
		}
	}
}
