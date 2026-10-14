package sql_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestNativeLiteralRegexFiltersAvoidProjections(t *testing.T) {
	ctx := context.Background()
	for _, source := range []string{
		`name~"^A"`, `name!~"^A"`, `name:matches("^A")`,
		`!name~"^A"`, `!name!~"^A"`, `!name:matches("^A")`,
		`age>18 name~"^A"`, `name~"^A" | name!~"^B"`,
	} {
		q, err := hffsql.Build(ctx, compile(t, source), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), "OFFSET 0") || strings.Contains(q.Where(), "SELECT") {
			t.Errorf("%s retains predicate projections: %s", source, q.Where())
		}
		for _, arg := range q.Args() {
			if arg == "^A" || arg == "^B" {
				t.Errorf("%s retained an unused original pattern: %#v", source, q.Args())
			}
		}
	}
	for _, source := range []string{`name~"["`, `name!~"["`, `name:matches("[")`, `name~null`, `name!~null`, `name:matches(null)`} {
		q, err := hffsql.Build(ctx, compile(t, source), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(source, err)
		}
		if q.Where() != "FALSE" || len(q.Args()) != 0 {
			t.Errorf("%s must be a false filter without unused parameters: %s %#v", source, q.Where(), q.Args())
		}
	}
}

func TestDirectRegexKeepsValidationAndValueContexts(t *testing.T) {
	ctx := context.Background()
	for _, source := range []string{`name~"\u0000"`, `name!~"\u0000"`, `name:matches("\u0000")`, `name:matches("[\u0000")`} {
		_, err := hffsql.Build(ctx, compile(t, source), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		detail := requireCode(t, err, hff.BackendError)
		if detail.Message != "PostgreSQL text cannot encode invalid UTF-8 or NUL" || detail.Span == nil {
			t.Fatal(source, detail)
		}
	}
	for _, source := range []string{`name!:matches("^A")`, `name:lower:matches("^A")`, `name:matches("^"):type="null"`, `(name~"^")=null`} {
		q, err := hffsql.Build(ctx, compile(t, source), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "SELECT") {
			t.Errorf("%s unexpectedly used direct filter lowering", source)
		}
	}
	for _, source := range []string{`name~name`, `name:matches(name)`} {
		p := compile(t, source)
		_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		detail := requireCode(t, err, hff.BackendError)
		n := p.Root()
		if source == `name:matches(name)` {
			n, _ = n.Child(0)
		}
		span := n.Span()
		if detail.Span == nil || *detail.Span != span {
			t.Errorf("%s changed its whole-expression diagnostic: %#v", source, detail.Span)
		}
	}
	q, err := hffsql.Build(ctx, compile(t, `age>18 name~"^A" age<100 name="Alice"`), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q.Args(), []any{int64(18), `(?:\A\u0041)`, int64(100), "Alice"}) {
		t.Fatal("translated pattern changed surrounding argument order", q.Args())
	}
}

func TestCallbackRegexKeepsItsGenericScope(t *testing.T) {
	ctx := context.Background()
	e, mappings := mixedPredicateEngine(t)
	for _, source := range []string{`tags:any(tag=>name:matches("^A"))`, `tags:any(tag=>!name!:matches("^A"))`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if err != nil {
			t.Fatal(source, err)
		}
		original := false
		for _, arg := range q.Args() {
			original = original || arg == "^A"
		}
		if !original || !strings.Contains(q.Where(), "regexp_like(") {
			t.Fatal("callback regex unexpectedly bypassed scoped value lowering", source, q.Where(), q.Args())
		}
	}
}
