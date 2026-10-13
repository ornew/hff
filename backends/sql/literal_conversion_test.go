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

func literalConversionEngine(t *testing.T) (*engine.Engine, []hffsql.Option) {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Int("age")), schema.Nullable(schema.Bool("flag")),
		schema.Nullable(schema.String("name")), schema.Nullable(schema.Bytes("blob")),
		schema.Nullable(schema.Duration("elapsed")), schema.Nullable(schema.Float("ratio")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var bindings []hffsql.Binding
	for _, name := range []string{"age", "flag", "name", "blob", "elapsed", "ratio"} {
		bindings = append(bindings, hffsql.Bind(hff.Path{name}, hffsql.Column("records", name)))
	}
	return e, []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)}
}

func TestLiteralConversionsKeepTypedPredicatesDirect(t *testing.T) {
	e, options := literalConversionEngine(t)
	for _, tc := range []struct {
		source string
		args   []any
	}{
		{`age>"18":number flag="true":bool name=42:string`, []any{int64(18), true, "42"}},
		{`elapsed>"0.25h0.75h":duration blob="YQ==":frombase64`, []any{int64(3600000000000), "61"}},
		{`name:contains(true:string,"YQ==":frombase64:base64)`, []any{"true", "YQ=="}},
		{`name:len>"2":number`, []any{int64(2)}},
		{`age:in("18":number,"21":number,"bad":number)`, []any{int64(18), int64(21)}},
		{`blob:in("YQ":frombase64r,"Yg==":frombase64)`, []any{"61", "62"}},
		{`flag:in("bad":bool)`, nil},
		{`name=null:string`, []any{"null"}},
		{`name="2026-01-01T12:00:00+09:00":timestamp:string`, []any{"2026-01-01T03:00:00Z"}},
		{`blob elapsed`, nil},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(context.Background(), p, options...)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if strings.Contains(q.Where(), "SELECT") || strings.Contains(q.Where(), "OFFSET 0") {
			t.Errorf("%s retained per-row conversion/projection: %s", tc.source, q.Where())
		}
		if !reflect.DeepEqual(q.Args(), tc.args) {
			t.Errorf("%s: arguments %#v, want %#v", tc.source, q.Args(), tc.args)
		}
	}
}

func TestLiteralConversionPreservesRejections(t *testing.T) {
	e, options := literalConversionEngine(t)
	for _, source := range []string{
		`flag="\u0000":bool`, `blob="\u0000":frombase64`, `elapsed="\u0000":duration`,
		`age="\u0000":number`, `name:contains("\u0000":string,null)`,
		`name="2026-01-01T00:00:00Z":string:timestamp:string`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, options...)
		if err == nil {
			t.Errorf("%s bypassed the original text/profile support restriction", source)
		}
	}
	restricted, err := engine.New(engine.WithSchema(e.Schema()), engine.WithCapabilities(capabilities.Standard().Without(capabilities.Method("string.duration"))))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restricted.Compile(context.Background(), `elapsed>"1h":duration`); err == nil {
		t.Fatal("constant duration bypassed capability validation")
	}
}

func TestLiteralConversionsLeaveFloatAndExtensionsToBackend(t *testing.T) {
	e, options := literalConversionEngine(t)
	p, err := e.Compile(context.Background(), `ratio>"1e-400":number name=1.0:string`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, options...)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), "pg_input_is_valid") || !strings.Contains(q.Where(), "double precision") {
		t.Fatal("Float conversion replaced native backend behavior")
	}
	ds := extensionDeclarations(t)
	ext := extensionEngine(t, ds)
	p, err = ext.Compile(context.Background(), `name="x":nullarg(null):string`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hffsql.Build(context.Background(), p, extensionOptions(ds)[:2]...); err == nil {
		t.Fatal("constant-looking extension bypassed implementation checks")
	}
	calls := 0
	translator := func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
		calls++
		return c.Expression(schema.BoolType, "{} IS NULL", args[0])
	}
	opts := append(extensionOptions(ds)[:2], hffsql.WithMethods(hffsql.Implement(ds[2], hffsql.PostgreSQL, translator)))
	if _, err := hffsql.Build(context.Background(), p, opts...); err != nil || calls != 1 {
		t.Fatalf("extension translation contract: calls=%d err=%v", calls, err)
	}
}
