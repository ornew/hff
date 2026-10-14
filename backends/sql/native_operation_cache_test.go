package sql_test

import (
	"context"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func assertOperationArgumentsUsed(t testing.TB, query *hffsql.Query) {
	t.Helper()
	used := make(map[int]bool)
	for _, match := range regexp.MustCompile(`\$([0-9]+)`).FindAllStringSubmatch(query.Where(), -1) {
		index, _ := strconv.Atoi(match[1])
		if index < 1 || index > len(query.Args()) {
			t.Fatal("placeholder has no argument", index, query.Where(), query.Args())
		}
		used[index] = true
	}
	for index := range query.Args() {
		if !used[index+1] {
			t.Fatal("argument has no surviving placeholder", index+1, query.Where(), query.Args())
		}
	}
}

func TestNativeOperationCacheKeepsOrderedArguments(t *testing.T) {
	ctx := context.Background()
	e, mappings := nativeColumnEngine(t, "nullable")
	for _, fixture := range []struct {
		source string
		args   []any
	}{
		{`i+2>10 i+2<20`, []any{int64(2), int64(10), int64(20)}},
		{`i+j>10 i+j<20`, []any{int64(10), int64(20)}},
		{`(i+2)*3>10 (i+2)*3<20`, []any{int64(2), int64(3), int64(10), int64(20)}},
		{`i-2>10 i-2<20`, []any{int64(2), int64(10), int64(20)}},
		{`i*2>10 i*2<20`, []any{int64(2), int64(10), int64(20)}},
		{`i/2>10.0 i/2<20.0`, []any{int64(2), float64(10), float64(20)}},
		{`i%2=0 i%2!=1`, []any{int64(2), int64(0), int64(1)}},
		{`(-i)>10 (-i)<20`, []any{int64(10), int64(20)}},
		{`i+2>10 s="between" i+2<20`, []any{int64(2), int64(10), "between", int64(20)}},
		{`i+2>10 i+3<20`, []any{int64(2), int64(10), int64(3), int64(20)}},
		{`i+2>10 i-2<20`, []any{int64(2), int64(10), int64(2), int64(20)}},
		{`i+"2":number>10 i+"2":number<20`, []any{int64(2), int64(10), int64(20)}},
	} {
		p, err := e.Compile(ctx, fixture.source)
		if err != nil {
			t.Fatal(fixture.source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if err != nil {
			t.Fatal(fixture.source, err)
		}
		if !reflect.DeepEqual(q.Args(), fixture.args) {
			t.Errorf("%s: args %v, want %v", fixture.source, q.Args(), fixture.args)
		}
		assertOperationArgumentsUsed(t, q)
	}
}

func TestNativeOperationCacheKeepsUnicodeAndScope(t *testing.T) {
	ctx := context.Background()
	e, mappings := nativeColumnEngine(t, "nullable")
	for _, fixture := range []struct {
		source     string
		translates int
	}{
		{`s:lower="a" s:lower="b"`, 1},
		{`s:upper="A" s:upper="B"`, 1},
		{`s:lower="a" s:upper="B"`, 2},
		{`s:lower="a" t:lower="b"`, 2},
		{`s:lower:upper="A" s:lower:upper="B"`, 2},
		{`s:lower="a" s!:lower="b"`, 2},
		{`s=["A":lower,"A":lower]:json`, 1},
		{`items:any(x=>s:lower="a" s:lower="b")`, 1},
		{`items:any(x=>s:lower="a") items:any(x=>s:lower="b")`, 2},
		{`items:any(x=>s:lower="a" items:any(x=>s:lower="b" s:lower="c"))`, 2},
	} {
		p, err := e.Compile(ctx, fixture.source)
		if err != nil {
			t.Fatal(fixture.source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if err != nil {
			t.Fatal(fixture.source, err)
		}
		if count := strings.Count(q.Where(), "translate("); count != fixture.translates {
			t.Errorf("%s: translate count %d, want %d", fixture.source, count, fixture.translates)
		}
		assertOperationArgumentsUsed(t, q)
	}
}

func TestNativeOperationCacheDoesNotEvaluateExtensionsAsConstants(t *testing.T) {
	ctx := context.Background()
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "service.identity.v1", Name: "customidentity", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("i"))), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))), engine.WithMethods(d))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `i+2>1 i:customidentity+2>1 i:customidentity+2<9 i+2<10`)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"i"}, hffsql.Column("records", "i"))), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL,
		func(c *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			bias, err := c.Parameter(hff.Int(7))
			if err != nil {
				return hffsql.Expression{}, err
			}
			return c.Expression(schema.IntType, "{}+{}", receiver, bias)
		})))
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 || !reflect.DeepEqual(q.Args(), []any{int64(2), int64(1), int64(7), int64(2), int64(1), int64(7), int64(2), int64(9), int64(10)}) {
		t.Fatal("extension dispatch or surrounding argument order changed", calls, q.Args())
	}
	assertOperationArgumentsUsed(t, q)
}

func TestNativeOperationCacheKeepsTextValidation(t *testing.T) {
	ctx := context.Background()
	e, mappings := nativeColumnEngine(t, "nullable")
	for _, source := range []string{`s:lower="a" s:lower="\u0000"`, `i+"2":number>1 i+"2\u0000":number<10`, `s=["A":lower,"A\u0000":lower]:json`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if requireCode(t, err, hff.BackendError).Span == nil {
			t.Fatal("validation error lost source location", source)
		}
	}
}
