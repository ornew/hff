package sql_test

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func nativeScalarFoldSources(itemRoleFixture) []string {
	values := `labels:items:map(p=>p[1])`
	textBool := values + `:flatmap(v=>[v:type,v:type:len>4])`
	bytesInt := values + `:flatmap(v=>["YQ==":frombase64,v:type:len])`
	durationInt := values + `:flatmap(v=>["1s":duration,v:type:len])`
	timestampInt := values + `:flatmap(v=>["2026-01-01T00:00:00Z":timestamp,v:type:len])`
	return []string{
		textBool + `:foldl("",(a,x)=>a:concat(x:string)):len>0`,
		textBool + `:foldr("",(x,a)=>a:concat(x:string)):len>0`,
		textBool + `:foldl([],(a,x)=>a:concat([x:type])):size>0`,
		textBool + `:foldl(0,(a,x)=>a+x:string:len+other:items:map(p=>p[0]:len):sum)>0`,
		bytesInt + `:foldl("",(a,x)=>a:concat(x:string)):len>0`,
		bytesInt + `:foldr(0,(x,a)=>a+x:string:len)>0`,
		durationInt + `:foldl("",(a,x)=>a:concat(x:string)):len>0`,
		durationInt + `:foldr(0,(x,a)=>a+x:string:len)>0`,
		timestampInt + `:foldl("",(a,x)=>a:concat(x:string)):len>0`,
		timestampInt + `:foldr(0,(x,a)=>a+x:string:len)>0`,
		values + `:flatmap(v=>[false,""]):foldl(true,(a,x)=>a x:exists)`,
		values + `:flatmap(v=>["A\nあ😀",false]):foldl(0,(a,x)=>a+x:string:len)>0`,
		values + `:flatmap(v=>["":frombase64,false]):foldl(true,(a,x)=>a x:exists)`,
		values + `:flatmap(v=>[v:type:duration,1]):foldl("",(a,x)=>a:concat(x:type)):contains("null")`,
		values + `:flatmap(v=>["9999-12-31T23:59:59.999999999Z":timestamp:add("1ns":duration),1]):foldl("",(a,x)=>a:concat(x:type)):contains("null")`,
		values + `:flatmap(v=>["-2562047h47m16.854775808s":duration,"2562047h47m16.854775807s":duration,1]):foldl(true,(a,x)=>a (x:[duration]:exists | x:[int]:exists))`,
		values + `:flatmap(v=>["0000-01-01T00:00:00.000000001Z":timestamp,"9999-12-31T23:59:59.999999999Z":timestamp,1]):foldl(true,(a,x)=>a (x:[timestamp]:exists | x:[int]:exists))`,
		textBool + `:foldl("",(a,x)=>[1]:foldl(a,(b,n)=>b:concat(x:string))):len>0`,
		textBool + `:foldl("",(a,x)=>a:concat(x:string):concat([1]:foldl("",(b,x)=>b:concat(x:string)))):len>0`,
		values + `:map(v=>v!):filter(v=>false):flatmap(v=>["1ns":duration,false]):foldl("seed",(a,x)=>a:concat(x:string))="seed"`,
		values + `:flatmap(v=>[]:concat(["1ns":duration,1])):foldl("",(a,x)=>a:concat(x:string)):len>0`,
		values + `:flatmap(v=>["1ns":duration,1]:concat([])):foldr("",(x,a)=>a:concat(x:string)):len>0`,
		values + `:flatmap(v=>["1ns":duration,1]):foldl(null!,(a,x)=>x:string):exists`,
		values + `:flatmap(v=>["1ns":duration,1]):foldl("",(a,x)=>null!):exists`,
		values + `:filter(v=>false):flatmap(v=>["1ns":duration,1]):foldl("seed",(a,x)=>x:string)="seed"`,
	}
}

func TestNativeScalarDeferredFoldsBuild(t *testing.T) {
	for _, tc := range deferredValueFixtures() {
		e := itemFieldFoldEngine(t, tc)
		for _, source := range nativeScalarFoldSources(tc) {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(tc.name, source, err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
				if err != nil {
					t.Fatal(tc.name, source, format, err)
				}
				requireConcatArgsUsed(t, q)
			}
		}
	}
}

func TestNativeScalarDeferredFoldSQLUsesTypedColumns(t *testing.T) {
	// Use a separate root object so deferred values originate in an items()
	// pipeline, rather than a literal list already handled by native folds.
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("labels", schema.Struct(schema.Bytes("asset"), schema.String("name"))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		values, columns string
	}{
		{`[v:type,v:type:len>4]`, "bool_value,string_value"},
		{`["YQ==":frombase64,v:type:len]`, "int_value,bytes_value"},
		{`["1ns":duration,v:type:len]`, "int_value,duration_value"},
		{`["0000-01-01T00:00:00.000000001Z":timestamp,v:type:len]`, "int_value,timestamp_value"},
		{`[v:type:len,1.0,false,v:type,"YQ==":frombase64,"1ns":duration,"0000-01-01T00:00:00.000000001Z":timestamp]`, "int_value,float_value,bool_value,string_value,bytes_value,duration_value,timestamp_value"},
	} {
		source := `labels:items:map(p=>p[1]):flatmap(v=>` + test.values + `):foldl("",(a,x)=>a:concat(x:string)):exists`
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)))
			if err != nil {
				t.Fatal(source, err)
			}
			if !strings.Contains(q.Where(), "SELECT arm,"+test.columns+",row_number()") {
				t.Fatal(source, "input still crosses a generic JSON packet", q.Where())
			}
			requireConcatArgsUsed(t, q)
		}
	}
}

func allByteBase64() string {
	value := make([]byte, 256)
	for i := range value {
		value[i] = byte(i)
	}
	return base64.StdEncoding.EncodeToString(value)
}

func TestNativeScalarFoldRetainsSourceErrorsAndExtensions(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.scalar.touch", Name: "touch", Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e := unusedFoldEngine(t, d)
	for i, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type:touch,false]):foldl("",(a,x)=>a:concat(x:string)):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type,false]):foldl("seed":touch,(a,x)=>a:concat(x:string)):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type,false]):foldl("",(a,x)=>a:touch:concat(x:string)):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		calls := 0
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, r hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			calls++
			return c.Expression(schema.StringType, "{}", r)
		})))
		if err != nil || calls != []int{1, 1, 2}[i] || strings.Contains(q.Where(), "AS bool_value") {
			t.Fatal("custom translation behavior changed", source, calls, err)
		}
		requireConcatArgsUsed(t, q)
	}
	for _, source := range []string{
		`labels:items:map(p=>p[1]):flatmap(v=>["\u0000",false]):foldl("",(a,x)=>a:concat(x:string)):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type,false]):foldl("\u0000",(a,x)=>a:concat(x:string)):exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type,false]):foldl("",(a,x)=>a:concat(x:string):concat("\u0000")):exists`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
		requireCode(t, err, hff.BackendError)
		var diagnostic *hff.Error
		if q != nil || !errors.As(err, &diagnostic) || diagnostic.Span == nil || diagnostic.Span.Start != strings.Index(source, `"\u0000"`) {
			t.Fatal("original diagnostic changed", source, err)
		}
	}
}

func TestNativeScalarFoldCancellation(t *testing.T) {
	e := unusedFoldEngine(t)
	p, err := e.Compile(context.Background(), `labels:items:map(p=>p[1]):flatmap(v=>["1ns":duration,v:type,false]):foldl("",(a,x)=>a:concat(x:string)):exists`)
	if err != nil {
		t.Fatal(err)
	}
	options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON))}
	baseline := &buildCancellationContext{Context: context.Background()}
	if _, err := hffsql.Build(baseline, p, options...); err != nil {
		t.Fatal(err)
	}
	for after := int64(1); after <= baseline.checks.Load(); after++ {
		base, cancel := context.WithCancel(context.Background())
		ctx := &buildCancellationContext{Context: base, after: after, cancel: cancel}
		q, err := hffsql.Build(ctx, p, options...)
		cancel()
		requireCode(t, err, hff.CancelledError)
		if q != nil || !errors.Is(err, context.Canceled) {
			t.Fatal("partial native scalar query escaped cancellation", after, q, err)
		}
	}
}
