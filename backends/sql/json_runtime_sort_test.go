package sql_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func runtimeSortSources(itemRoleFixture) []string {
	const fold = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	const other = `other:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))`
	return []string{
		fold + `:sort:exists`,
		fold + `:sort:type="null"`,
		fold + `:sort:json=` + other + `:sort:json`,
		fold + `:sort:map(v=>v:type):json=` + fold + `:map(v=>v:type):sort:json`,
		fold + `:sort:sort=` + fold + `:sort`,
		fold + `:sort[0]:type=` + fold + `:sort[-1]:type`,
		fold + `:sort:unique`,
		fold + `:sort:distinct:json=` + fold + `:sort:json`,
		fold + `:filter(v=>v:[number]:exists):sort:map(v=>v:type):json:exists`,
		fold + `:sort:map(v=>v!):exists`,
		fold + `:sort[0]=labels.app`,
		fold + `:sort[-1]=labels.z`,
		`labels:items:map(p=>p[1]):sort:json=` + fold + `:sort:json`,
		`labels:items:foldl([], (a,p)=>a:concat([p])):sort[0][1]:type="null"`,
	}
}

func TestRuntimeSortBuild(t *testing.T) {
	for _, tc := range runtimeSortFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeSortSources(tc) {
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

func runtimeSortFixtures() []itemRoleFixture {
	fixtures := deferredValueFixtures()
	for i := range fixtures {
		tc := &fixtures[i]
		var values []hff.Value
		switch tc.name {
		case "duration/map":
			values = []hff.Value{hff.Duration(time.Duration(math.MinInt64)), hff.Duration(-1), hff.Duration(0), hff.Duration(1), hff.Duration(time.Duration(math.MaxInt64))}
		case "timestamp/map":
			values = []hff.Value{hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), hff.Timestamp(time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC)), hff.Timestamp(time.Date(1970, 1, 1, 0, 0, 0, 0, time.UTC)), hff.Timestamp(time.Date(1970, 1, 1, 0, 0, 0, 1, time.UTC)), hff.Timestamp(time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC))}
		}
		for j, v := range values {
			w := values[len(values)-j-1]
			object := func(a, z hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"app": a, "z": z}) }
			tc.extraPairs = append(tc.extraPairs, [2]hff.Value{object(v, w), object(w, v)})
		}
	}
	return fixtures
}

func runtimeSortAnySources(itemRoleFixture) []string {
	const first = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v]))[0]:[list[any]]`
	const sorted = first + `:sort`
	return []string{
		sorted + `:exists`, sorted + `:type="null"`, sorted + `:json="[-0.0,0]"`, sorted + `:json="[0,-0.0]"`,
		sorted + `:map(v=>v:type):json="[\"float\",\"int\"]"`, sorted + `[0]:type="int"`,
		sorted + `[0]=` + first + `[0]`, sorted + `[-1]=` + first + `[-1]`,
		sorted + `:sort:json=` + sorted + `:json`,
		first + `:concat(` + first + `):sort:json=` + sorted + `:concat(` + sorted + `):sort:json`,
		first + `:filter(v=>v:[number]:exists):sort:map(v=>v:type):json:exists`,
		sorted + `:map(v=>v!):exists`,
		`other:items:any(p=>` + sorted + `:map(v=>v:in(p[1])):all(v=>v))`,
	}
}

func runtimeSortEdgeFixture() itemRoleFixture {
	tc := runtimeMembershipEdgeFixture()
	for _, values := range [][]hff.Value{
		{hff.Int(9007199254740993), hff.Float(9007199254740992), hff.Int(9007199254740992)},
		{hff.Int(-9007199254740993), hff.Float(-9007199254740992), hff.Int(-9007199254740992)},
		{hff.Float(4503599627370496), hff.Int(4503599627370497), hff.Int(4503599627370495), hff.Float(-4503599627370496), hff.Int(-4503599627370497)},
		{hff.Int(math.MaxInt64), hff.Float(9223372036854775808), hff.Int(math.MinInt64), hff.Float(-9223372036854775808)},
		{hff.Float(0.9999999999999999), hff.Int(1), hff.Float(1.0000000000000002)},
		{hff.Float(math.MaxFloat64), hff.Float(math.SmallestNonzeroFloat64), hff.Int(0), hff.Float(-math.SmallestNonzeroFloat64)},
		{hff.String("z"), hff.String("é"), hff.String("e\u0301"), hff.String("a")},
		{hff.Bool(false)}, {hff.Null()},
	} {
		for _, reverse := range []bool{false, true} {
			v := append([]hff.Value(nil), values...)
			if reverse {
				for i, j := 0, len(v)-1; i < j; i, j = i+1, j-1 {
					v[i], v[j] = v[j], v[i]
				}
			}
			o := hff.Object(map[string]hff.Value{"app": hff.List(v...), "z": tc.second})
			tc.extraPairs = append(tc.extraPairs, [2]hff.Value{o, o})
		}
	}
	return tc
}

func TestRuntimeSortAnyBuild(t *testing.T) {
	for _, tc := range append(itemAnyFixtures(), runtimeSortEdgeFixture()) {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range runtimeSortAnySources(tc) {
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

func TestRuntimeSortCancellation(t *testing.T) {
	tc := runtimeSortEdgeFixture()
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), runtimeSortAnySources(tc)[4])
	if err != nil {
		t.Fatal(err)
	}
	opts := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, true)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, opts...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		ctx, cancel := context.WithCancel(context.Background())
		probe := &buildCancellationContext{Context: ctx, after: after, cancel: cancel}
		q, err := hffsql.Build(probe, p, opts...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("partial %v, error %v", q, err)
		}
	}
}

func TestRuntimeSortRetainsFloatLoss(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Float("app"), schema.Bytes("z"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	const prefix = `labels:items:map(p=>p[1]):foldl([], (a,v)=>a:concat([v])):filter(v=>v:[number]:exists):sort`
	for _, suffix := range []string{`:json:exists`, `:map(v=>v:type):json:exists`, `[0]>0`} {
		p, err := e.Compile(context.Background(), prefix+suffix)
		if err != nil {
			t.Fatal(err)
		}
		for _, metadata := range []bool{false, true} {
			var opts []hffsql.JSONBindingOption
			if metadata {
				opts = append(opts, hffsql.WithTypeMetadata(hffsql.Column("records", "meta")))
			}
			_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSONB, opts...)))
			if suffix == `:json:exists` && !metadata {
				requireCode(t, err, hff.BackendError)
			} else if err != nil {
				t.Fatal(suffix, metadata, err)
			}
		}
	}
}

func TestRuntimeSortKeepsNativeStoredPath(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("values", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `values:sort`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"values"}, hffsql.Column("values"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.Where(), "WITH RECURSIVE") || strings.Contains(q.Where(), "'timestamp'") {
		t.Fatal("ordinary stored sort acquired a typed tree")
	}
}
