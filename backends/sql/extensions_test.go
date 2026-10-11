package sql_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func extensionDeclarations(t *testing.T) []hff.MethodDeclaration {
	t.Helper()
	configs := []hff.MethodConfig{
		{ID: "service.prefix.v1", Name: "customprefix", Receiver: schema.StringType, Parameters: []hff.Type{schema.StringType}, Result: schema.BoolType, BaseCost: 1},
		{ID: "service.nullseen.v1", Name: "nullseen", Receiver: schema.AnyType, Result: schema.BoolType, NullPolicy: hff.InvokeNull, BaseCost: 1},
		{ID: "service.nullarg.v1", Name: "nullarg", Receiver: schema.StringType, Parameters: []hff.Type{hff.NewUnionType(schema.StringType, schema.NullType)}, Result: schema.BoolType, NullPolicy: hff.InvokeNull, BaseCost: 1},
		{ID: "service.allpresent.v1", Name: "allpresent", Receiver: schema.StringType, Parameters: []hff.Type{schema.StringType, schema.StringType}, Result: schema.BoolType, BaseCost: 1},
	}
	out := make([]hff.MethodDeclaration, len(configs))
	for i, c := range configs {
		d, err := hff.NewMethodDeclaration(c)
		if err != nil {
			t.Fatal(err)
		}
		out[i] = d
	}
	return out
}
func extensionEngine(t *testing.T, ds []hff.MethodDeclaration) *engine.Engine {
	t.Helper()
	caps := capabilities.Standard()
	for _, d := range ds {
		caps = caps.Allow(capabilities.Method(d.ID()))
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.String("name")))), engine.WithMethods(ds...), engine.WithCapabilities(caps))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func prefixTranslator(c *hffsql.Call, r hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
	return c.Expression(schema.BoolType, `left({},char_length({})) COLLATE "C" = {} COLLATE "C"`, r, args[0], args[0])
}
func extensionOptions(ds []hff.MethodDeclaration) []hffsql.Option {
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))), hffsql.WithMethods(
		hffsql.Implement(ds[0], hffsql.PostgreSQL, prefixTranslator),
		hffsql.Implement(ds[1], hffsql.PostgreSQL, func(c *hffsql.Call, r hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "{} IS NULL", r)
		}),
		hffsql.Implement(ds[2], hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, a []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "{} IS NULL", a[0])
		}),
		hffsql.Implement(ds[3], hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Parameter(hff.Bool(true))
		}),
	)}
}
func TestSQLTranslatorConfigurationAndBinding(t *testing.T) {
	ds := extensionDeclarations(t)
	e := extensionEngine(t, ds)
	ctx := context.Background()
	p, err := e.Compile(ctx, `name:customprefix("O'Reilly")`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, p, extensionOptions(ds)...)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Where(), "O'Reilly") || !reflect.DeepEqual(query.Args(), []any{"O'Reilly"}) {
		t.Fatal(query.Where(), query.Args())
	}
	if !strings.Contains(query.Where(), "IS NOT NULL") || !strings.Contains(query.Where(), "CASE") {
		t.Fatal("missing framework NULL propagation")
	}
	base := extensionOptions(ds)[:2]
	bad := []hffsql.MethodImplementation{{}, hffsql.Implement(ds[0], "other", prefixTranslator), hffsql.Implement(ds[0], hffsql.PostgreSQL, nil)}
	for _, m := range bad {
		_, err := hffsql.Build(ctx, p, append(base, hffsql.WithMethods(m))...)
		requireCode(t, err, hff.ConfigurationError)
	}
	m := hffsql.Implement(ds[0], hffsql.PostgreSQL, prefixTranslator)
	_, err = hffsql.Build(ctx, p, append(base, hffsql.WithMethods(m, m))...)
	requireCode(t, err, hff.ConfigurationError)
	wrong, _ := hff.NewMethodDeclaration(hff.MethodConfig{ID: ds[0].ID(), Name: ds[0].Name(), Receiver: schema.StringType, Parameters: []hff.Type{schema.StringType}, Result: schema.BoolType, BaseCost: 2})
	_, err = hffsql.Build(ctx, p, append(base, hffsql.WithMethods(hffsql.Implement(wrong, hffsql.PostgreSQL, prefixTranslator)))...)
	if requireCode(t, err, hff.BackendError).Span == nil {
		t.Fatal("missing signature mismatch span")
	}
	methods := []hffsql.MethodImplementation{m}
	option := hffsql.WithMethods(methods...)
	methods[0] = hffsql.MethodImplementation{}
	if _, err := hffsql.Build(ctx, p, append(base, option)...); err != nil {
		t.Fatal("option aliases input", err)
	}
	concurrentOptions := append(append([]hffsql.Option(nil), base...), option)
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 8 {
				if _, err := hffsql.Build(ctx, p, concurrentOptions...); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
}
func TestSQLTranslationContractFailures(t *testing.T) {
	ds := extensionDeclarations(t)
	p, err := extensionEngine(t, ds).Compile(context.Background(), `name:customprefix("Al")`)
	if err != nil {
		t.Fatal(err)
	}
	cause := errors.New("translator failure")
	var retained *hffsql.Call
	var foreign hffsql.Expression
	good := func(c *hffsql.Call, r hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
		retained = c
		foreign = r
		return prefixTranslator(c, r, args)
	}
	opts := extensionOptions(ds)[:2]
	if _, err := hffsql.Build(context.Background(), p, append(opts, hffsql.WithMethods(hffsql.Implement(ds[0], hffsql.PostgreSQL, good)))...); err != nil {
		t.Fatal(err)
	}
	_, err = retained.Parameter(hff.Int(1))
	requireCode(t, err, hff.BackendError)
	cases := []struct {
		name string
		fn   hffsql.MethodFunc
	}{
		{"zero", func(*hffsql.Call, hffsql.Expression, []hffsql.Expression) (hffsql.Expression, error) {
			return hffsql.Expression{}, nil
		}},
		{"foreign result", func(*hffsql.Call, hffsql.Expression, []hffsql.Expression) (hffsql.Expression, error) {
			return foreign, nil
		}},
		{"wrong type", func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Parameter(hff.Int(1))
		}},
		{"foreign input", func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "{} IS NULL", foreign)
		}},
		{"wrong arity", func(c *hffsql.Call, r hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "{} = {}", r)
		}},
		{"manual parameter", func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "$1 IS NULL")
		}},
		{"invalid template", func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.BoolType, "\x00")
		}},
		{"unsupported expression type", func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.AnyType, "TRUE")
		}},
		{"invalid parameter", func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Parameter(hff.Value{})
		}},
		{"ignored helper error", func(c *hffsql.Call, r hffsql.Expression, a []hffsql.Expression) (hffsql.Expression, error) {
			value, err := prefixTranslator(c, r, a)
			if err != nil {
				return value, err
			}
			_, _ = c.Parameter(hff.String("\x00"))
			return value, nil
		}},
		{"error", func(*hffsql.Call, hffsql.Expression, []hffsql.Expression) (hffsql.Expression, error) {
			return hffsql.Expression{}, cause
		}},
		{"panic", func(*hffsql.Call, hffsql.Expression, []hffsql.Expression) (hffsql.Expression, error) { panic(cause) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hffsql.Build(context.Background(), p, append(opts, hffsql.WithMethods(hffsql.Implement(ds[0], hffsql.PostgreSQL, tc.fn)))...)
			detail := requireCode(t, err, hff.BackendError)
			if detail.Span == nil {
				t.Fatal("missing source span")
			}
			if (tc.name == "error" || tc.name == "panic") && !errors.Is(err, cause) {
				t.Fatal("lost cause")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	fn := func(c *hffsql.Call, r hffsql.Expression, a []hffsql.Expression) (hffsql.Expression, error) {
		v, err := prefixTranslator(c, r, a)
		cancel()
		return v, err
	}
	_, err = hffsql.Build(ctx, p, append(opts, hffsql.WithMethods(hffsql.Implement(ds[0], hffsql.PostgreSQL, fn)))...)
	requireCode(t, err, hff.CancelledError)
}
func TestSQLTranslatorPreservesHostCancellationCause(t *testing.T) {
	ds := extensionDeclarations(t)
	p, err := extensionEngine(t, ds).Compile(context.Background(), `name:customprefix("Al")`)
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []string{"value", "status", "cause"} {
		t.Run(result, func(t *testing.T) {
			cause := errors.New("host stopped SQL translation")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			fn := func(c *hffsql.Call, r hffsql.Expression, a []hffsql.Expression) (hffsql.Expression, error) {
				value, err := prefixTranslator(c, r, a)
				if err != nil {
					return value, err
				}
				cancel(cause)
				switch result {
				case "status":
					return hffsql.Expression{}, c.Context().Err()
				case "cause":
					return hffsql.Expression{}, context.Cause(c.Context())
				}
				return value, nil
			}
			query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("name"))),
				hffsql.WithMethods(hffsql.Implement(ds[0], hffsql.PostgreSQL, fn)))
			requireCode(t, err, hff.CancelledError)
			if query != nil || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("lost translator cancellation cause or returned a query: %v", err)
			}
		})
	}
}

func TestSQLTranslatorParametersAndExpressionMetadata(t *testing.T) {
	ds := extensionDeclarations(t)
	p, err := extensionEngine(t, ds).Compile(context.Background(), `name:customprefix("Al")`)
	if err != nil {
		t.Fatal(err)
	}
	translator := func(c *hffsql.Call, r hffsql.Expression, a []hffsql.Expression) (hffsql.Expression, error) {
		if !r.Valid() || r.Type().Kind() != hff.UnionType || c.Dialect() != hffsql.PostgreSQL || c.Context() == nil {
			t.Fatal("invalid metadata")
		}
		yes, err := c.Parameter(hff.Bool(true))
		if err != nil {
			return yes, err
		}
		prefix, err := prefixTranslator(c, r, a)
		if err != nil {
			return prefix, err
		}
		return c.Expression(schema.BoolType, "{} AND {}", yes, prefix)
	}
	q, err := hffsql.Build(context.Background(), p, append(extensionOptions(ds)[:2], hffsql.WithMethods(hffsql.Implement(ds[0], hffsql.PostgreSQL, translator)))...)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q.Args(), []any{"Al", true}) {
		t.Fatal(q.Args())
	}
}
func TestSQLExecutionGuarantees(t *testing.T) {
	g, err := hffsql.ExecutionGuarantees(hffsql.PostgreSQL)
	if err != nil || g.MaxSteps || g.MaxGeneratedValues {
		t.Fatal(g, err)
	}
	_, err = hffsql.ExecutionGuarantees("unknown")
	requireCode(t, err, hff.ConfigurationError)
	p := compile(t, `age>1`)
	base := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...)}
	for _, limits := range []hffsql.ExecutionLimits{{MaxSteps: 1}, {MaxGeneratedValues: 1}} {
		_, err = hffsql.Build(context.Background(), p, append(base, hffsql.WithExecutionLimits(limits))...)
		requireCode(t, err, hff.BackendError)
	}
	for _, limits := range []hffsql.ExecutionLimits{{MaxSteps: -1}, {MaxGeneratedValues: -1}} {
		_, err = hffsql.Build(context.Background(), p, append(base, hffsql.WithExecutionLimits(limits))...)
		requireCode(t, err, hff.ConfigurationError)
	}
	q, err := hffsql.Build(context.Background(), p, append(base, hffsql.WithExecutionLimits(hffsql.ExecutionLimits{}))...)
	if err != nil || q.Guarantees() != g {
		t.Fatal(q, err)
	}
}

func localExtensionOptions(ds []hff.MethodDeclaration) []local.Option {
	return []local.Option{local.WithMethods(
		local.Implement(ds[0], func(c *local.Call, r hff.Value, a []hff.Value) (hff.Value, error) {
			s, _ := r.AsString()
			p, _ := a[0].AsString()
			if err := c.ChargeSteps(len(s) + len(p)); err != nil {
				return hff.Null(), err
			}
			return hff.Bool(strings.HasPrefix(s, p)), nil
		}),
		local.Implement(ds[1], func(_ *local.Call, r hff.Value, _ []hff.Value) (hff.Value, error) {
			return hff.Bool(r.Kind() == hff.NullKind), nil
		}),
		local.Implement(ds[2], func(_ *local.Call, _ hff.Value, a []hff.Value) (hff.Value, error) {
			return hff.Bool(a[0].Kind() == hff.NullKind), nil
		}),
		local.Implement(ds[3], func(_ *local.Call, _ hff.Value, _ []hff.Value) (hff.Value, error) { return hff.Bool(true), nil }),
	)}
}

func TestSQLOverloadsAndOptionalArguments(t *testing.T) {
	configs := []hff.MethodConfig{
		{ID: "service.select.int", Name: "selectarg", Receiver: schema.StringType, Parameters: []hff.Type{schema.IntType}, Result: schema.BoolType, BaseCost: 1},
		{ID: "service.select.string", Name: "selectarg", Receiver: schema.StringType, Parameters: []hff.Type{schema.StringType}, Result: schema.BoolType, BaseCost: 1},
		{ID: "service.arguments", Name: "arguments", Receiver: schema.StringType, Optional: []hff.Type{schema.IntType}, Variadic: schema.StringType, Result: schema.BoolType, BaseCost: 1},
	}
	ds := make([]hff.MethodDeclaration, len(configs))
	for i, c := range configs {
		var err error
		ds[i], err = hff.NewMethodDeclaration(c)
		if err != nil {
			t.Fatal(err)
		}
	}
	e := extensionEngine(t, ds)
	for _, source := range []string{`name:selectarg(1)`, `name:selectarg("one")`, `name:arguments`, `name:arguments(1)`, `name:arguments(1,"a","b")`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		called := []string{}
		methods := make([]hffsql.MethodImplementation, len(ds))
		for i, d := range ds {
			methods[i] = hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
				called = append(called, d.ID())
				if d.Name() == "arguments" {
					expected := 0
					if strings.Contains(source, "(1") {
						expected = 1
					}
					if strings.Contains(source, `"b"`) {
						expected = 3
					}
					if len(args) != expected {
						t.Fatalf("%s arguments=%d expected=%d", source, len(args), expected)
					}
				}
				return c.Parameter(hff.Bool(true))
			})
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))), hffsql.WithMethods(methods...))
		if err != nil {
			t.Fatal(source, err)
		}
		if len(called) != 1 {
			t.Fatal("unexpected static dispatch", called)
		}
		if strings.Contains(source, "selectarg(1)") && called[0] != ds[0].ID() {
			t.Fatal(called)
		}
		if strings.Contains(source, `selectarg("one")`) && called[0] != ds[1].ID() {
			t.Fatal(called)
		}
	}
}
