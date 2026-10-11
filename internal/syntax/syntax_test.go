package syntax

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/conformance"
)

type admissionContext struct {
	context.Context
	checks int
	cancel context.CancelCauseFunc
	cause  error
}

func (c *admissionContext) Err() error {
	c.checks++
	if c.checks == 4 {
		c.cancel(c.cause)
	}
	return c.Context.Err()
}

func TestQueryUTF8AdmissionCancellation(t *testing.T) {
	ctx, cancel := context.WithCancelCause(context.Background())
	defer cancel(nil)
	cause := errors.New("stop query admission")
	probe := &admissionContext{Context: ctx, cancel: cancel, cause: cause}
	tree, err := ParseQuery(probe, `name="`+strings.Repeat("東京😀", 4096)+"\xff\"", Config{})
	var detail *hff.Error
	if tree != nil || !errors.As(err, &detail) || detail.Code != hff.CancelledError || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
		t.Fatalf("query admission did not preserve cancellation: %v", err)
	}
}

func TestPreflightCancellationAcrossEscapes(t *testing.T) {
	for _, tc := range []struct{ name, source string }{
		{"string", `x="` + strings.Repeat(`\\`, 2045) + `"`},
		{"quoted name", "`" + strings.Repeat(`\\`, 2047) + "`"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tree, err := ParseQuery(context.Background(), tc.source, Config{}); err != nil || tree == nil {
				t.Fatalf("escape fixture must remain a valid query: %v", err)
			}
			ctx, cancel := context.WithCancelCause(context.Background())
			cause := errors.New("stop escaped query preflight")
			probe := &admissionContext{Context: ctx, cancel: cancel, cause: cause}
			err := preflight(probe, tc.source, 64)
			cancel(nil)
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != hff.CancelledError || !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("escaped bytes skipped cancellation checks: %v", err)
			}
		})
	}
}

func TestGrammarConformance(t *testing.T) {
	valid := []string{
		``, " \t\r\n", `foo.age>10 foo.father.name~"^A"`,
		"`service.name`=\"auth-service\" `null`=false",
		`(age>=10 | age<=5) name="Alice"`, `age>=10 | age<=5 name="Alice"`,
		`!sex="Female"`, `!flag`, `a!=b a!~b`, `a[-1]`,
		`n1!.n2!.n3!.nullvalue=null`, `n1!.n2!.value:exists()`,
		`{"a": "12h":duration()}`, `[1:tostring()]`,
		`items:map(x=>x.age)`, `items:foldl(0,(acc,x)=>acc+x)`,
		`items:any(x=>x.price>10 x.active)`, `items:map((x=>x.age))`,
		`items:map(x=>x.children:map(y=>y.name))`,
		`flag=(1<2)`, `obj[key]`, `a -b`, `a--b`,
		`[ ]`, `{ }`, `a:in( 1 , 2 )`, `a| b`, `a |b`,
		"`日本語`=\"😀\"", "``", "`a\\`b\\\\c`", "`a\nb`",
		`name="\ud83d\ude00"`, `name="\\ud800"`, `age=1e-3`,
		`x:[string]:contains("foo")`, `x:[list[string|null]]`,
		`(x+1):[int]`, `x:[(string | list[map[int]])]`,
		`name:lower:len>3`, `value:exists`, `x:type="string"`,
		`x:keys[-1]`, `x:size!`, `x:len (y)`, `x:contains`,
	}
	for _, source := range valid {
		t.Run(source, func(t *testing.T) {
			if _, err := ParseQuery(context.Background(), source, Config{}); err != nil {
				t.Fatalf("valid syntax rejected: %v", err)
			}
		})
	}
	invalid := []string{
		`age > 10`, `age> 10`, `age >10`, `a +b`, `a+ b`,
		`a .b`, `a: size()`, `a:size ()`, `! a`, `a !`,
		`a<b<c`, `a=b=c`, `[1,]`, `{"a":1,}`, `a:in(1,)`,
		`# comment`, `a // comment`, `x=>x.age`, `a=01`, `a=.5`,
		`name="\ud800"`, `name="\udc00"`, `name="\ud800\u0041"`,
		"`a\\n`", "name=\"a\nb\"", "a=\xff", `a|`, `()`,
		`x :[string]`, `x: [string]`, `x:[]`, `x:[list[]]`,
		`x:[string|]`, `x:[list[int,]]`,
		`x:len(`, `x:len(,)`, `x: len`, `x:len)`,
	}
	for _, source := range invalid {
		t.Run(source, func(t *testing.T) {
			_, err := ParseQuery(context.Background(), source, Config{})
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != hff.SyntaxError {
				t.Fatalf("want syntax error, got %v", err)
			}
		})
	}
}

func unwrap(x Expr) Expr {
	for {
		switch n := x.(type) {
		case *Or:
			if len(n.Terms) == 1 {
				x = n.Terms[0]
				continue
			}
		case *And:
			if len(n.Terms) == 1 {
				x = n.Terms[0]
				continue
			}
		case *Access:
			if len(n.Parts) == 0 {
				x = n.Base
				continue
			}
		}
		return x
	}
}

func TestPrecedence(t *testing.T) {
	parse := func(s string) Expr {
		t.Helper()
		x, err := ParseQuery(context.Background(), s, Config{})
		if err != nil {
			t.Fatal(err)
		}
		return unwrap(x)
	}
	n := parse(`!age+2*3>=10`).(*Not)
	comparison := unwrap(n.X).(*Binary)
	if comparison.Op.Text != ">=" {
		t.Fatalf("wrong comparison: %#v", comparison.Op)
	}
	add := unwrap(comparison.Left).(*Binary)
	mul := unwrap(add.Right).(*Binary)
	if add.Op.Text != "+" || mul.Op.Text != "*" {
		t.Fatal("arithmetic precedence")
	}
	or := parse(`age>=10 | age<=5 name="Alice"`).(*Or)
	if len(or.Terms) != 2 || len(unwrap(or.Terms[1]).(*And).Terms) != 2 {
		t.Fatal("AND must bind before OR")
	}
	sub := parse(`age-2-3`).(*Binary)
	if unwrap(sub.Left).(*Binary).Op.Text != "-" {
		t.Fatal("subtraction must be left associative")
	}
}

func TestLimitsAndLocations(t *testing.T) {
	for _, source := range []string{`((age))`, `!!age`, `age.foo.bar`, `age+1+2`} {
		if _, err := ParseQuery(context.Background(), source, Config{Limits: hff.Limits{MaxSyntaxDepth: 2}}); err != nil {
			t.Fatalf("depth two %q: %v", source, err)
		}
		_, err := ParseQuery(context.Background(), source, Config{Limits: hff.Limits{MaxSyntaxDepth: 1}})
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.LimitError {
			t.Fatalf("depth limit %q: %v", source, err)
		}
	}
	_, err := ParseQuery(context.Background(), `name="😀"`, Config{Limits: hff.Limits{MaxQueryBytes: 10}})
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != hff.LimitError {
		t.Fatalf("byte limit: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = ParseQuery(ctx, `age`, Config{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation: %v", err)
	}
	x, err := ParseQuery(context.Background(), "`日本`", Config{})
	if err != nil {
		t.Fatal(err)
	}
	if got := ExprSpan(unwrap(x)); got.Start != 0 || got.End != len("`日本`") {
		t.Fatalf("byte span: %+v", got)
	}
	location := sourceError(hff.SyntaxError, "日本\na😀?", len("日本\na😀"), "bad").(*hff.Error)
	if location.Line != 2 || location.Column != 3 || location.Span.Start != 12 {
		t.Fatalf("location: %+v", location)
	}
}

func FuzzParseQuery(f *testing.F) {
	for _, tc := range conformance.Cases() {
		f.Add(tc.Source)
	}
	for _, s := range []string{`age>10 name`, `items:any(x=>x!)`, `x:[map[list[string|null]]]`, `"\ud800"`, strings.Repeat("(", 65), "\xff"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, source string) {
		_, err := ParseQuery(context.Background(), source, Config{Limits: hff.Limits{MaxQueryBytes: 4096, MaxSyntaxDepth: 32}})
		if err != nil {
			var detail *hff.Error
			if !errors.As(err, &detail) {
				t.Fatalf("untyped error: %v", err)
			}
			if detail.Span != nil && (detail.Span.Start < 0 || detail.Span.End > len(source)) {
				t.Fatalf("invalid error span: %+v", detail.Span)
			}
		}
	})
}
