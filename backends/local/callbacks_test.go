package local_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestCallbackMethods(t *testing.T) {
	cases := []struct {
		query string
		value hff.Value
		want  bool
	}{
		{`x:any(v=>v)`, hff.List(), false},
		{`x:all(v=>v)`, hff.List(), true},
		{`x:any(v=>v)`, hff.List(hff.Int(0), hff.Int(2)), true},
		{`x:all(v=>v)`, hff.List(hff.Int(2), hff.Int(0)), false},
		{`x:any(v=>v!)`, hff.List(hff.Null(), hff.Int(2)), true},
		{`x:all(v=>v!)`, hff.List(hff.Int(2), hff.Null()), false},
		{`x:any(v=>v:exists())`, hff.List(hff.Bool(false)), true},
		{`x:any(v=>v)=null`, hff.Null(), true},
		{`x!:any(v=>v)=null`, hff.Null(), false},
		{`x:map(v=>v+1)=[2,3]`, hff.List(hff.Int(1), hff.Int(2)), true},
		{`x:map(v=>1)=[1,1]`, hff.List(hff.Null(), hff.Int(2)), true},
		{`x:map(v=>v/0)=[null,null]`, hff.List(hff.Int(1), hff.Int(2)), true},
		{`x:map(v=>v)=[null,1]`, hff.List(hff.Null(), hff.Int(1)), true},
		{`x:map(v=>v!)=[null,1]`, hff.List(hff.Null(), hff.Int(1)), false},
		{`x:map(v=>v!)=null`, hff.List(hff.Null(), hff.Int(1)), false},
		{`x:map(v=>v)=[]`, hff.List(), true},
		{`x:flatmap(v=>[v,v])=[1,1,2,2]`, hff.List(hff.Int(1), hff.Int(2)), true},
		{`x:flatmap(v=>v)=[1,null,2]`, hff.List(hff.List(hff.Int(1), hff.Null()), hff.List(hff.Int(2))), true},
		{`x:flatmap(v=>v)=null`, hff.List(hff.List(hff.Int(1)), hff.Int(2)), true},
		{`x:flatmap(v=>v)=null`, hff.List(hff.List(hff.Int(1)), hff.Null()), true},
		{`x:flatmap(v=>v!)=null`, hff.List(hff.Null()), false},
		{`x:flatmap(v=>v)=[]`, hff.List(), true},
		{`x:foldl(0,(a,v)=>a+v)=6`, hff.List(hff.Int(1), hff.Int(2), hff.Int(3)), true},
		{`x:foldr(0,(v,a)=>v-a)=2`, hff.List(hff.Int(1), hff.Int(2), hff.Int(3)), true},
		{`x:foldl(0,(a,v)=>a-v)=-6`, hff.List(hff.Int(1), hff.Int(2), hff.Int(3)), true},
		{`x:foldl(7,(a,v)=>a+v)=7`, hff.List(), true},
		{`x:foldr(null,(v,a)=>v)=null`, hff.List(), true},
		{`x:foldl(null,(a,v)=>v)=3`, hff.List(hff.Int(1), hff.Null(), hff.Int(3)), true},
		{`x:foldr(null,(v,a)=>v)=1`, hff.List(hff.Int(1), hff.Null(), hff.Int(3)), true},
		{`x:foldl(0,(a,v)=>v!):exists()`, hff.List(hff.Int(1), hff.Null(), hff.Int(3)), false},
		{`x:foldl([], (a,v)=>a:concat([v]))=[1,2]`, hff.List(hff.Int(1), hff.Int(2)), true},
		{`x:foldl(0,(a,v)=>[a])=[[[0]]]`, hff.List(hff.Int(1), hff.Int(2), hff.Int(3)), true},
		{`x:map(x=>x:map(x=>x+1))=[[2,3],[4]]`, hff.List(hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Int(3))), true},
		{`x:map(v=>v:map(w=>w+v:size()))=[[3,4],[4]]`, hff.List(hff.List(hff.Int(1), hff.Int(2)), hff.List(hff.Int(3))), true},
	}
	for _, tc := range cases {
		t.Run(tc.query, func(t *testing.T) {
			f := build(t, tc.query)
			got, err := f.Match(context.Background(), record(tc.value, hff.Null()))
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v want %v", got, err, tc.want)
			}
		})
	}
}

func TestCallbackBudgetsAndShortCircuit(t *testing.T) {
	ctx := context.Background()
	long := hff.String("a very long string whose lowercase conversion exceeds the tiny budget")
	for _, q := range []string{`x:any(v=>v:exists() | y:lower()="a")`, `x:all(v=>v! y:lower()="a")`} {
		f := build(t, q, local.WithLimits(hff.Limits{MaxSteps: 20}))
		x := hff.List(hff.Null(), long)
		want := false
		if q == `x:any(v=>v:exists() | y:lower()="a")` {
			x = hff.List(hff.Int(1), hff.Null())
			want = true
		}
		got, err := f.Match(ctx, record(x, long))
		if err != nil || got != want {
			t.Fatal(q, got, err)
		}
	}
	for _, q := range []string{`x:map(v=>v)`, `x:flatmap(v=>[v,v])`} {
		f := build(t, q, local.WithLimits(hff.Limits{MaxGeneratedValues: 2}))
		_, err := f.Match(ctx, record(hff.List(hff.Int(1), hff.Int(2)), hff.Null()))
		code(t, err, hff.LimitError)
	}
	f := build(t, `x:map(v=>v!)=null`, local.WithLimits(hff.Limits{MaxGeneratedValues: 1}))
	got, err := f.Match(ctx, record(hff.List(hff.Null(), hff.Int(2)), hff.Null()))
	if err != nil || got {
		t.Fatalf("guard skipped later allocations: %v %v", got, err)
	}
	f = build(t, `x:foldl(0,(a,v)=>a+v)`, local.WithLimits(hff.Limits{MaxSteps: 4}))
	_, err = f.Match(ctx, record(hff.List(hff.Int(1), hff.Int(2)), hff.Null()))
	code(t, err, hff.LimitError)
}

func TestNestedCollectionPredicates(t *testing.T) {
	item := func(price int64, flag bool, tags ...hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{
			"price": hff.Int(price), "flag": hff.Bool(flag), "tags": hff.List(tags...),
		})
	}
	// AND binds more tightly than OR inside a callback, just as at the root.
	f := build(t, `x:any(x=>x.price>10 x.flag | x.tags:all(tag=>tag="production"))`)
	for _, tc := range []struct {
		items hff.Value
		want  bool
	}{
		{hff.List(item(20, true, hff.String("development"))), true},
		{hff.List(item(5, false, hff.String("production"))), true},
		{hff.List(item(20, false, hff.String("development"))), false},
		{hff.List(item(5, true, hff.String("production"), hff.String("development"))), false},
	} {
		got, err := f.Match(context.Background(), record(tc.items, hff.Null()))
		if err != nil || got != tc.want {
			t.Fatalf("nested predicate: got %v %v want %v", got, err, tc.want)
		}
	}
	// All iterations, including those of an inner callback, share one budget.
	bounded := build(t, `x:any(row=>row:all(v=>v))`, local.WithLimits(hff.Limits{MaxSteps: 30}))
	values := make([]hff.Value, 100)
	for i := range values {
		values[i] = hff.Bool(true)
	}
	_, err := bounded.Match(context.Background(), record(hff.List(hff.List(values...)), hff.Null()))
	code(t, err, hff.LimitError)
}

func TestCallbackCaptureAndConcurrency(t *testing.T) {
	ctx := context.Background()
	f := build(t, `x:map(v=>v+y)=[11,12] x:map(x=>x+y)=[11,12]`)
	rec := record(hff.List(hff.Int(1), hff.Int(2)), hff.Int(10))
	var wg sync.WaitGroup
	for range 12 {
		wg.Go(func() {
			for range 30 {
				got, err := f.Match(ctx, rec)
				if err != nil || !got {
					t.Errorf("callback state leaked: %v %v", got, err)
					return
				}
			}
		})
	}
	wg.Wait()
}

type countingContext struct {
	context.Context
	calls  atomic.Int64
	cancel context.CancelFunc
}

func (c *countingContext) Err() error {
	if c.calls.Add(1) > 12 {
		c.cancel()
	}
	return c.Context.Err()
}
func TestCallbackCancellationAndFlatmapType(t *testing.T) {
	e := configured(t)
	p, err := e.Compile(context.Background(), `x:flatmap(v=>v)`)
	if err != nil {
		t.Fatal(err)
	}
	call, _ := p.Root().Child(0)
	expected := hff.NewUnionType(hff.NewListType(hff.PrimitiveType(hff.AnyType)), hff.PrimitiveType(hff.NullType))
	if !call.Type().Equal(expected) {
		t.Fatalf("Any flatmap result type: %v", call.Type())
	}
	p, err = e.Compile(context.Background(), `x:map(v=>v+1)`)
	if err != nil {
		t.Fatal(err)
	}
	f, err := local.Build(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	values := make([]hff.Value, 100)
	for i := range values {
		values[i] = hff.Int(int64(i))
	}
	input, err := e.Validate(context.Background(), record(hff.List(values...), hff.Null()))
	if err != nil {
		t.Fatal(err)
	}
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &countingContext{Context: base, cancel: cancel}
	yes, err := f.MatchInput(ctx, input)
	code(t, err, hff.CancelledError)
	if yes {
		t.Fatal("cancelled callback returned partial match")
	}
}
