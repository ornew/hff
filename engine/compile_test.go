package engine_test

import (
	"context"
	"errors"
	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
	"sync"
	"testing"
)

func compilerSchema() hff.Type {
	return schema.Struct(schema.String("name"), schema.Int("age"), schema.Bool("flag"), schema.Any("x"), schema.List("items", schema.IntType), schema.Map("labels", schema.StringType), schema.Field("mixed", schema.Union(schema.StringType, schema.ListOf(schema.StringType))), schema.Nullable(schema.String("maybe")))
}
func TestCompileConformance(t *testing.T) {
	e := newEngine(t, compilerSchema())
	valid := []string{"", `age>10 name~"^A"`, `(age>=10 | age<=5) name="Alice"`, `!name="Female"`, `age name`, `labels.env="prod"`, `labels["app"]="auth"`, `items[-1]>10`, `items[1.0]`, `age+10<10`, `age>(10+2)`, `(10+age)<20`, `flag=(1<2)`, `x:[string]:contains("foo")`, `x:[list[string]]:contains("foo")`, `(age+1):[int]:exists()`, `x:[(string|list[int])]:exists()`, `x:[map[list[string|null]]]:size()`, `items:map(x=>x+age)`, `items:any(x=>x>10)`, `items:all(x=>x!)`, `items:flatmap(x=>[x,age])`, `items:foldl(0,(acc,x)=>acc+x)`, `items:foldr(0,(x,acc)=>x+acc)`, `items:map(x=>[x]:map(y=>y+age))`, `items:map(age=>age+1)`, `items:map(x=>1)`, `items:map(x=>null:size())`, `items:map(x=>null+null)`, `items:map(x=>{"a":"12h":duration()})`, `age=-9223372036854775808`, `age=9223372036854775808`, `age!`, `maybe=null`, `maybe!=null`}
	for _, source := range valid {
		t.Run(source, func(t *testing.T) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			if !p.Valid() || p.Root().Type().Kind() != hff.BoolType || p.Root().MayNoValue() {
				t.Fatalf("invalid predicate plan: %+v", p.Root())
			}
			if p.Source() != source || !p.Schema().Equal(e.Schema()) {
				t.Fatal("program lost source/schema")
			}
		})
	}
	invalid := []struct {
		source string
		code   hff.ErrorCode
	}{
		{`1+1`, hff.TypeError}, {`10<age`, hff.TypeError}, {`age>10 | true`, hff.TypeError}, {`missing>10`, hff.TypeError}, {`name.missing`, hff.TypeError}, {`name+1`, hff.TypeError}, {`age~"foo"`, hff.TypeError}, {`items[true]`, hff.TypeError}, {`labels[1]`, hff.TypeError}, {`name:contains()`, hff.TypeError}, {`name:contains(1)`, hff.TypeError}, {`age:len()`, hff.TypeError}, {`name:nope()`, hff.TypeError}, {`items:map((x,y)=>x)`, hff.TypeError}, {`items:foldl(0,(x,x)=>x)`, hff.TypeError}, {`items:flatmap(x=>x)`, hff.TypeError}, {`[1]:any(x=>x>0)`, hff.TypeError}, {`items:map(x=>{"a":1,"a":2})`, hff.TypeError}, {`x:[unknown]`, hff.TypeError}, {`x:[string[int]]`, hff.TypeError}, {`x:[map]`, hff.TypeError}, {`x:[list[int,string]]`, hff.TypeError}, {`age > 10`, hff.SyntaxError}, {`age<10<20`, hff.SyntaxError}, {`x :[string]`, hff.SyntaxError},
	}
	for _, tc := range invalid {
		t.Run(tc.source, func(t *testing.T) {
			_, err := e.Compile(context.Background(), tc.source)
			detail := requireCode(t, err, tc.code)
			if detail.Span == nil {
				t.Fatal("missing error span")
			}
		})
	}
}
func findNode(n hff.Node, kind hff.NodeKind) (hff.Node, bool) {
	if n.Kind() == kind {
		return n, true
	}
	for i := 0; i < n.ChildCount(); i++ {
		child, _ := n.Child(i)
		if found, ok := findNode(child, kind); ok {
			return found, true
		}
	}
	return hff.Node{}, false
}
func TestTypeGuardCapabilitiesAndIR(t *testing.T) {
	policy := capabilities.Standard().Without(capabilities.Method("list.contains"))
	restricted := newEngine(t, compilerSchema(), engine.WithCapabilities(policy))
	for _, source := range []string{`mixed:contains("foo")`, `x:contains("foo")`, `mixed:[list[string]]:contains("foo")`} {
		_, err := restricted.Compile(context.Background(), source)
		requireCode(t, err, hff.CapabilityError)
	}
	for _, source := range []string{`mixed:[string]:contains("foo")`, `x:[string]:contains("foo")`, `maybe:contains("foo")`} {
		p, err := restricted.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		call, _ := findNode(p.Root(), hff.CallNode)
		method, _ := call.Method()
		sigs := method.Signatures()
		if len(sigs) != 1 || sigs[0].ID() != "string.contains" {
			t.Fatalf("%s: signatures %+v", source, sigs)
		}
	}
	p, err := restricted.Compile(context.Background(), `x:[list[int|float]]:size()`)
	if err != nil {
		t.Fatal(err)
	}
	guard, ok := findNode(p.Root(), hff.TypeGuardNode)
	if !ok || !guard.MayNoValue() {
		t.Fatal("missing no-value guard")
	}
	constraint, ok := guard.Constraint()
	if !ok || constraint.Kind() != hff.ListType || !guard.Type().Equal(constraint) {
		t.Fatal("guard constraint lost")
	}
	noObjects := newEngine(t, compilerSchema(), engine.WithCapabilities(capabilities.Standard().Without(capabilities.Method("object.size"))))
	_, err = noObjects.Compile(context.Background(), `items:map(x=>null:size())`)
	requireCode(t, err, hff.CapabilityError)
	_, err = noObjects.Compile(context.Background(), `items:map(x=>null:[list[int]|null]:size())`)
	if err != nil {
		t.Fatal(err)
	}
}
func TestCompileCallbacksAndCancellation(t *testing.T) {
	e := newEngine(t, compilerSchema())
	p, err := e.Compile(context.Background(), `items:map(x=>x+age)`)
	if err != nil {
		t.Fatal(err)
	}
	cb, _ := findNode(p.Root(), hff.CallbackNode)
	params, _ := cb.Parameters()
	captures, _ := cb.Captures()
	if len(params) != 1 || params[0].Name() != "x" || len(captures) != 1 || captures[0].Name() != "age" {
		t.Fatal("wrong callback bindings")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.Compile(ctx, `age>10`)
	requireCode(t, err, hff.CancelledError)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := e.Compile(context.Background(), `items:any(x=>x>age)`); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
}

func TestCompileNumericAndNullPlans(t *testing.T) {
	e := newEngine(t, compilerSchema())
	for _, tc := range []struct {
		source string
		want   hff.Kind
	}{
		{`age=-9223372036854775808`, hff.IntKind},
		{`age=9223372036854775808`, hff.NullKind},
		{`age=1.0`, hff.FloatKind},
		{`age=1e9999`, hff.NullKind},
	} {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		binary, _ := findNode(p.Root(), hff.BinaryNode)
		right, _ := binary.Child(1)
		value, ok := right.Literal()
		if !ok || value.Kind() != tc.want {
			t.Fatalf("%s literal %v", tc.source, value.Kind())
		}
	}
	for _, source := range []string{`maybe=null`, `maybe!=null`, `maybe!=(null)`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		if p.Root().Kind() != hff.NullTestNode {
			t.Fatal("null test not explicit in IR")
		}
	}
}
func FuzzCompile(f *testing.F) {
	e, err := engine.New(engine.WithSchema(compilerSchema()), engine.WithCapabilities(capabilities.Standard()), engine.WithLimits(hff.Limits{MaxQueryBytes: 4096, MaxSyntaxDepth: 32}))
	if err != nil {
		f.Fatal(err)
	}
	for _, s := range []string{`age>10`, `x:[list[int|null]]:contains(1)`, `items:foldl(0,(a,x)=>a+x)`, `items:map(x=>[x]:map(y=>age+y))`, `x:[unknown]`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, source string) {
		p, err := e.Compile(context.Background(), source)
		if err == nil {
			if !p.Valid() || p.Root().Type().Kind() != hff.BoolType {
				t.Fatal("invalid compiled predicate")
			}
		} else {
			var detail *hff.Error
			if !errors.As(err, &detail) {
				t.Fatalf("untyped compiler error: %v", err)
			}
			if detail.Span != nil && (detail.Span.Start < 0 || detail.Span.End > len(source) || detail.Span.End < detail.Span.Start) {
				t.Fatal("invalid diagnostic span")
			}
		}
	})
}

func TestCompileStepBudget(t *testing.T) {
	e := newEngine(t, compilerSchema(), engine.WithLimits(hff.Limits{MaxSteps: 1}))
	_, err := e.Compile(context.Background(), `age>10`)
	requireCode(t, err, hff.LimitError)
}

func TestCompileBuiltinFamilies(t *testing.T) {
	typ := schema.Struct(schema.Bool("b"), schema.Int("n"), schema.Float("f"), schema.String("s"), schema.Bytes("bytes"), schema.List("ls", schema.IntType), schema.Map("obj", schema.IntType), schema.Timestamp("ts"), schema.Duration("d"))
	e := newEngine(t, typ)
	queries := []string{`s:type()`, `s:tostring()`, `s:tojson()`, `s:in("x")`, `s:exists()`, `b:and(true)`, `b:or(false)`, `b:not()`, `n:round()`, `f:ceil()`, `f:floor()`, `s:len()`, `bytes:len()`, `s:contains("a","b")`, `s:matches("x")`, `s:startswith("x")`, `s:endswith("x")`, `s:lower()`, `s:upper()`, `s:timestamp()`, `s:duration()`, `s:bool()`, `s:number()`, `s:fromjson()`, `s:concat("x")`, `s:frombase64()`, `bytes:tobase64()`, `ls:size()`, `ls:has(1.0)`, `ls:contains(1,2)`, `ls:unique()`, `ls:sort()`, `ls:concat([1.0])`, `ls:map(x=>x)`, `ls:flatmap(x=>[x])`, `ls:any(x=>x>0)`, `ls:all(x=>x>0)`, `ls:foldl(0,(a,x)=>a+x)`, `ls:foldr(0,(x,a)=>x+a)`, `obj:size()`, `obj:has("a")`, `obj:get("a")`, `obj:keys()`, `obj:values()`, `obj:items()`, `ts:year()`, `ts:month("Asia/Tokyo")`, `ts:dayofmonth()`, `ts:dayofyear()`, `ts:hours()`, `ts:minutes()`, `ts:seconds()`, `ts:milliseconds()`, `ts:string("UTC")`, `ts:after(ts)`, `ts:before(ts)`, `ts:add(d)`, `d:hours()`, `d:minutes()`, `d:seconds()`, `d:milliseconds()`, `d:microseconds()`, `d:nanoseconds()`}
	for _, source := range queries {
		t.Run(source, func(t *testing.T) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			call, ok := findNode(p.Root(), hff.CallNode)
			if !ok {
				t.Fatal("missing call")
			}
			method, _ := call.Method()
			if len(method.Signatures()) != 1 {
				t.Fatalf("wrong overload count for %s", source)
			}
		})
	}
}
