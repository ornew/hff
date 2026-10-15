package sql_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func deferredAggregateSources(itemRoleFixture) []string {
	const values = `labels:items:map(p=>p[1])`
	const fold = values + `:foldl([], (a,v)=>a:concat([v]))`
	var sources []string
	for _, method := range []string{"sum", "avg", "min", "max"} {
		sources = append(sources,
			fold+`:concat([1]):`+method+`:exists`,
			fold+`:`+method+`(v=>v:[number]):exists`,
			fold+`:map(v=>{"value":v}):`+method+`(o=>o.value:[number]):exists`,
			fold+`:filter(v=>false):`+method+`(v=>1):json:exists`,
			fold+`:map(v=>v!):`+method+`(v=>1):json:exists`,
			fold+`:map(v=>v!):filter(v=>false):`+method+`(v=>1):json:exists`,
			fold+`:`+method+`(v=>null!):json:exists`,
			fold+`:`+method+`(v=>[v,1][labels:size%2]):exists`,
			fold+`:`+method+`(v=>[{"value":v},1][labels:size%2]):json:exists`,
		)
	}
	return append(sources,
		`other:items:any(p=>`+fold+`:sum(v=>p[0]:len)=p[0]:len*`+fold+`:size)`,
		`other:items:any(p=>`+fold+`:sum(p=>p:[number]):exists p[0]="app")`,
		fold+`:map(v=>[v,1]):sum(xs=>xs:sum(x=>x:[number])):exists`,
	)
}

func deferredAggregateAnySources(itemRoleFixture) []string {
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	const items = fold + `[0]:[list[any]]`
	var sources []string
	for _, method := range []string{"sum", "avg", "min", "max"} {
		result := items + `:` + method
		sources = append(sources,
			result+`:exists`, result+`=null`, result+`:[int]:exists`, result+`:[float]:exists`,
			result+`:json="-0.0"`, result+`:json="0"`, result+`:json="1.0"`,
			items+`:`+method+`(v=>v!):exists`,
			items+`:`+method+`(v=>v:[number]):exists`,
		)
	}
	return sources
}

func deferredAggregateExtremeFixture() itemRoleFixture {
	tc := runtimeMembershipEdgeFixture()
	tc.name = "numeric-extremes/any-field"
	object := func(items hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"app": items, "z": tc.second})
	}
	for _, values := range [][]hff.Value{
		{hff.Int(9007199254740993), hff.Float(9007199254740992)},
		{hff.Int(math.MaxInt64), hff.Float(9223372036854775808)},
		{hff.Int(math.MinInt64), hff.Float(-9223372036854775808)},
		{hff.Float(math.Copysign(0, -1)), hff.Int(0), hff.Float(0)},
		{hff.Float(math.MaxFloat64), hff.Float(math.SmallestNonzeroFloat64)},
		{hff.Int(1), hff.Float(1), hff.Null()},
	} {
		left := hff.List(values...)
		reverse := append([]hff.Value(nil), values...)
		for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
			reverse[i], reverse[j] = reverse[j], reverse[i]
		}
		tc.extraPairs = append(tc.extraPairs, [2]hff.Value{object(left), object(hff.List(reverse...))}, [2]hff.Value{object(hff.List(reverse...)), object(left)})
	}
	return tc
}

func deferredAggregateExtremeSources(tc itemRoleFixture) []string {
	var sources []string
	for _, source := range deferredAggregateAnySources(tc) {
		if strings.Contains(source, ":min") || strings.Contains(source, ":max") {
			sources = append(sources, source)
		}
	}
	const items = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))[0]:[list[any]]`
	for _, method := range []string{"min", "max"} {
		for _, index := range []string{"0", "-1"} {
			sources = append(sources, items+`:`+method+`=`+items+`[`+index+`]`, items+`:`+method+`:json=`+items+`[`+index+`]:json`)
		}
	}
	return sources
}

func TestDeferredAggregateAnyBuild(t *testing.T) {
	for _, tc := range append(itemAnyFixtures(), runtimeMembershipEdgeFixture()) {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredAggregateAnySources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestDeferredAggregateFloatProvenance(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Float("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	for _, method := range []string{"sum", "avg", "min", "max"} {
		for _, observer := range []string{`:json:exists`, `:exists`, `:[int]:json:exists`} {
			source := fold + `:` + method + `(v=>v:[number])` + observer
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				for _, metadata := range []bool{false, true} {
					var options []hffsql.JSONBindingOption
					if format == hffsql.JSONB && metadata {
						options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
					}
					_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format, options...)))
					wantError := format == hffsql.JSONB && !metadata && (method == "min" || method == "max") && observer == `:json:exists`
					if (err != nil) != wantError {
						t.Errorf("%s %s metadata%v: %v", source, format, metadata, err)
					}
				}
			}
		}
	}
}

func TestDeferredAggregateCancellation(t *testing.T) {
	tc := runtimeMembershipEdgeFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), deferredAggregateSources(tc)[2])
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, tc.metadata)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("partial %v, error %v", q, err)
		}
	}
}

func TestDeferredAggregateZeroArmDiagnostics(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, receiver := range []string{`labels:items:map(p=>p[1]):flatmap(v=>null)`, `labels:items:foldl(null,(a,p)=>null)`} {
		for _, method := range []string{"sum", "avg", "min", "max"} {
			source := receiver + `:` + method + `(v=>"\u0000":number):exists`
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("labels"), hffsql.JSON)))
			if strings.Contains(receiver, "flatmap") {
				requireCode(t, err, hff.BackendError)
				if !strings.Contains(err.Error(), "NUL") {
					t.Fatal(source, err)
				}
			} else if err != nil {
				t.Fatal(source, err)
			}
		}
	}
}

func TestDeferredAggregateKeepsNativePaths(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("values", schema.IntType), schema.Map("labels", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`values:sum>0`, `values:avg>0`, `values:min>0`, `values:max>0`, `labels:items:sum(p=>p[0]:len)>0`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"values"}, hffsql.Column("values"), hffsql.JSON), hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("labels"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), `'["invalid",null]'`) {
			t.Fatal("native aggregate acquired typed contribution rows", source)
		}
	}
}

func TestDeferredAggregateBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredAggregateSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func deferredLengthSources(itemRoleFixture) []string {
	const values = `labels:items:map(p=>p[1])`
	const fold = values + `:foldl([], (a,v)=>a:concat([v]))`
	return []string{
		fold + `:map(v=>v:len):json=` + values + `:map(v=>v:len):json`,
		fold + `:sum(v=>v:len)=` + values + `:sum(v=>v:len)`,
		fold + `:map(v=>[v]):map(xs=>xs[0]:len):json=` + values + `:map(v=>v:len):json`,
		fold + `:map(v=>{"key":v}):map(o=>o.key:len):json=` + values + `:map(v=>v:len):json`,
		fold + `:map(v=>v!):map(v=>v:len):json:exists`,
		values + `:flatmap(v=>[v])[0]:len+1>0`,
		values + `:flatmap(v=>[v])[0]:len`,
		values + `:flatmap(v=>[v])[0]:len:[int]:exists`,
		fold + `:find(v=>true):len=1`,
		fold + `:sum(v=>[v]:filter(x=>true)[0]:len):exists`,
	}
}
func TestDeferredLengthBuild(t *testing.T) {
	for _, tc := range deferredLengthFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredLengthSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}
func TestDeferredAggregateCookbookBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("labels", schema.Struct(schema.Nullable(schema.Any("app")), schema.Nullable(schema.Bytes("asset")), schema.Nullable(schema.Duration("elapsed")), schema.Nullable(schema.Timestamp("recorded"))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const items = `labels:items:map(pair=>{"key":pair[0],"value":pair[1]}):foldl([], (acc,item)=>acc:concat([item]))`
	for _, suffix := range []string{`:sum(item=>item.key:len)=23`, `:avg(item=>item.key:len)=5.75`, `:min(item=>item.key:len)=3`, `:max(item=>item.key:len)=8`} {
		p, err := e.Compile(context.Background(), items+suffix)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("labels"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("metadata"))))); err != nil {
			t.Error(suffix, err)
		}
	}
}

// Include root Any unions and strings whose rune and byte lengths differ.
func deferredLengthFixtures() []itemRoleFixture {
	fixtures := append(deferredValueFixtures(), itemAnyFixtures()...)
	for i, member := range []hff.Type{schema.NullType, schema.IntType} {
		tc := itemAnyFixtures()[0]
		tc.name = []string{"length/any-null", "length/any-int"}[i]
		tc.typ = schema.NullableType(schema.Struct(schema.Optional(schema.Field("app", hff.NewUnionType(schema.AnyType, member))), schema.Optional(schema.Nullable(schema.Bytes("z")))))
		for _, text := range []string{"日本語", "é", "😀", ""} {
			object := hff.Object(map[string]hff.Value{"app": hff.String(text), "z": hff.Bytes([]byte{0, 255, 128, 1})})
			tc.extraPairs = append(tc.extraPairs, [2]hff.Value{object, object})
		}
		fixtures = append(fixtures, tc)
	}
	return fixtures
}

func deferredLengthNullQueries(t *testing.T) []*hffsql.Query {
	t.Helper()
	_, declaration := foldedNullExtension(t)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.NullableType(schema.StringType)))), engine.WithMethods(declaration), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `"x":nullcarrier(labels:items:foldl(null,(a,p)=>null):len)`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(declaration, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
		return c.Expression(schema.BoolType, "pg_typeof({})::text='unknown'", args[0])
	})))
	if err != nil {
		t.Fatal(err)
	}
	return []*hffsql.Query{pipelineNullCarrierQuery(t, "len"), q}
}

func TestDeferredLengthRetainsNullCarrier(t *testing.T) {
	for _, q := range deferredLengthNullQueries(t) {
		if !strings.Contains(q.Where(), "pg_typeof(NULL)") {
			t.Fatal("len changed its untyped null SQL carrier", q.Where())
		}
	}
}
