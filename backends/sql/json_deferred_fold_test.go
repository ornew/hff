package sql_test

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func deferredInputFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	types := values + `:map(v=>v:type):json`
	return []string{
		values + `:foldl([], (acc,v)=>acc:concat([v:type])):json=` + types,
		values + `:foldr([], (v,acc)=>[v:type]:concat(acc)):json=` + types,
		values + `:foldl(0, (acc,v)=>acc+v:type:len)>0`,
		values + `:foldl("", (acc,v)=>acc:concat(v:type)):len>0`,
		values + `:foldl(null, (acc,v)=>v:type):type="string"`,
		values + `:foldl([], (acc,v)=>acc:concat([v!:[any]:type])):json:exists`,
		values + `:map(x=>[x,[]]):foldl([], (acc,v)=>acc:concat(v:map(x=>x:type))):json:exists`,
		values + `:map(x=>[x,12,"not-a-number"]):foldl([], (acc,v)=>acc:concat(v:map(x=>x:type))):json:exists`,
		values + `:map(x=>[x]):foldl([], (acc,v)=>acc:concat([v[0]:type])):json=` + types,
		values + `:map(x=>{"payload":x}):foldl([], (acc,v)=>acc:concat([v.payload:type])):json=` + types,
		values + `:map(x=>{"payload":x}):foldr([], (v,acc)=>[v:get("payload"):type]:concat(acc)):json=` + types,
		values + `:flatmap(x=>[x]):foldl([], (acc,v)=>acc:concat([v:type])):json=` + types,
		values + `:map(x=>[x,null][labels:size%2]):foldl([], (acc,v)=>acc:concat([v:type])):json:exists`,
		values + `:map(x=>x!):filter(x=>false):foldl([], (acc,v)=>acc:concat([v:type])):json:exists`,
		values + `:foldr(null!, (v,acc)=>v:type):exists`,
		values + `:filter(x=>false):foldl("empty", (acc,v)=>v:type)="empty"`,
		values + `:map(x=>[x]):flatmap(xs=>[xs])[0]:foldl([], (acc,v)=>acc:concat([v:type])):json:exists`,
		`other:items:any(p=>` + values + `:foldl(0, (acc,v)=>acc+p[0]:len+v:type:len)>0)`,
		values + `:foldl([], (acc,v)=>other:items:map(v=>[acc:size,v[0]])):exists`,
	}
}

func TestDeferredInputFoldsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredInputFoldSources(tc) {
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

func TestDeferredInputFoldRetainsOrdinaryPath(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):foldl(0,(acc,v)=>acc+v:len)>0`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(q.Where(), "row_number()") {
		t.Fatal("ordinary fold unexpectedly uses packet dispatch")
	}
}

func TestDeferredInputFoldPreservesFloatLoss(t *testing.T) {
	for _, method := range []string{"type", "json"} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.BytesType), schema.List("groups", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):flatmap(x=>[x,groups[0]]):foldl("",(acc,v)=>v:`+method+`):exists`)
		if err != nil {
			t.Fatal(err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
				hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format),
				hffsql.BindJSON(hff.Path{"groups"}, hffsql.Column("records", "groups"), format)))
			reject := method == "json" && format == hffsql.JSONB
			if (err != nil) != reject {
				t.Errorf("%s %s reject%v: %v", method, format, reject, err)
			}
		}
	}
}

func TestDeferredInputFoldRejectsAmbiguousAccumulator(t *testing.T) {
	var tc itemRoleFixture
	for _, candidate := range deferredValueFixtures() {
		if candidate.name == "bytes/mixed" {
			tc = candidate
			break
		}
	}
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):foldl([], (acc,v)=>acc:concat([v])):map(v=>v:type):exists`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, false)...))
	requireCode(t, err, hff.BackendError)
}

func TestDeferredInputFoldCancellation(t *testing.T) {
	var tc itemRoleFixture
	for _, candidate := range deferredValueFixtures() {
		if candidate.name == "bytes/mixed" {
			tc = candidate
			break
		}
	}
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), deferredInputFoldSources(tc)[0])
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, false)...)}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for _, after := range []int64{1, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
		base, cancel := context.WithCancel(context.Background())
		ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
		q, err := hffsql.Build(ctx, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("checkpoint%d partial%v err%v", after, q, err)
		}
	}
}

func deferredWidePacketSource() string {
	fields := []string{`"payload":v`}
	for i := 0; i < 105; i++ {
		fields = append(fields, `"k`+strconv.Itoa(i)+`":`+strconv.Itoa(i))
	}
	return `labels:items:map(p=>p[1]):map(v=>{` + strings.Join(fields, ",") + `}):foldl([], (acc,v)=>acc:concat([v.payload:type])):json=labels:items:map(p=>p[1]):map(v=>v:type):json`
}

func TestDeferredInputFoldWideObjectAndDiagnostics(t *testing.T) {
	var tc itemRoleFixture
	for _, candidate := range deferredValueFixtures() {
		if candidate.name == "bytes/mixed" {
			tc = candidate
			break
		}
	}
	e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		deferredWidePacketSource(),
		`labels:items:map(p=>p[1]):flatmap(v=>null):foldl("",(acc,v)=>"x":concat("\u0000")):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>null):foldl(false,(acc,v)=>v:[string]:matches(".")):exists`,
		`labels:items:map(p=>p[1]):filter(v=>false):foldl("",(acc,v)=>v:json:concat("\u0000")):exists`,
		`labels:items:map(p=>p[1]):filter(v=>false):foldl(false,(acc,v)=>v:[string]:matches(".")):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, false)...))
		if source == deferredWidePacketSource() {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			requireCode(t, err, hff.BackendError)
		}
	}
}

func deferredZeroInputFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	return []string{
		values + `:flatmap(v=>null):foldl("seed",(acc,v)=>v:type)="seed"`,
		values + `:flatmap(v=>null):foldl("seed",(acc,v)=>v:type):type="null"`,
		values + `:flatmap(v=>null):foldr(null!, (v,acc)=>v:type):type="null"`,
		values + `:flatmap(v=>[]):foldl("seed",(acc,v)=>v:type)="seed"`,
		values + `:map(v=>v!):flatmap(v=>null):foldl("seed",(acc,v)=>v:type):type="null"`,
	}
}

func TestDeferredZeroInputFoldsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredZeroInputFoldSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Error(tc.name, format, source, err)
				}
			}
		}
	}
}

func TestDeferredInputFoldRequiresIdentityProof(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		if tc.name != "bytes/any-field" {
			continue
		}
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(context.Background(), deferredInputFoldSources(tc)[0])
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(hffsql.JSON, tc.metadata)...))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(q.Where(), "row_number()") {
			t.Fatal("Any erased tagged input identity through ordinary fold materialization")
		}
		return
	}
	t.Fatal("missing Any/Bytes fixture")
}

func deferredFoldAccumulatorRoleSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	return []string{
		values + `:foldl(other:items,(acc,v)=>acc:filter(p=>p[1]:type!="bytes")):map(p=>p[1]:type):contains("bytes")`,
		values + `:foldr(other:items,(v,acc)=>acc:filter(p=>p[1]:type!="bytes")):map(p=>p[1]:type):contains("bytes")`,
		values + `:foldl(other:items[-1],(acc,v)=>acc:[list[any]]):json=other:items[-1]:json`,
		values + `:foldr(other:items[-1],(v,acc)=>acc:[list[any]]):json=other:items[-1]:json`,
		values + `:foldl(other:items[-1],(acc,v)=>[acc]:find(p=>p[1]:type="bytes")):json=other:items[-1]:json`,
		values + `:foldr(null,(v,acc)=>other:items:filter(p=>p[1]:type!="bytes")):map(p=>p[1]:type):contains("bytes")`,
		values + `:filter(v=>false):foldl(other:items,(acc,v)=>acc:filter(p=>p[1]:type!="bytes")):json=other:items:json`,
		values + `:flatmap(v=>null):foldr(other:items,(v,acc)=>acc:filter(p=>p[1]:type!="bytes")):type="null"`,
	}
}

func TestDeferredFoldAccumulatorRolesBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		if tc.name != "bytes/any-field" {
			continue
		}
		e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range deferredFoldAccumulatorRoleSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				if _, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...)); err != nil {
					t.Error(format, source, err)
				}
			}
		}
		return
	}
	t.Fatal("missing Any/Bytes fixture")
}
