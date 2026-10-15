package sql_test

import (
	"context"
	"errors"
	"fmt"
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

func dynamicStructSchema() hff.Type {
	return schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(
		schema.Optional(schema.Nullable(schema.String("text"))),
		schema.Optional(schema.Nullable(schema.Bytes("asset"))),
		schema.Optional(schema.Nullable(schema.Duration("elapsed"))),
		schema.Optional(schema.Nullable(schema.Timestamp("recorded"))),
		schema.Optional(schema.Nullable(schema.Float("rate"))),
		schema.Optional(schema.Nullable(schema.Any("dynamic"))),
		schema.Optional(schema.Field("alwaysNull", schema.NullType)),
	))), schema.Any("key"))
}

func dynamicStructSources() []string {
	const object = `{"asset":data.asset,"text":data.text,"rate":data.rate,"elapsed":data.elapsed,"recorded":data.recorded,"dynamic":data.dynamic}`
	var sources []string
	for _, receiver := range []string{"data", object} {
		for _, access := range []string{`:get(key)`, `[key]`} {
			value := receiver + access
			for _, suffix := range []string{`:type="bytes"`, `:type="string"`, `:type="number"`, `:exists`, `=null`, `:[bytes]:base64="YQ=="`, `:[bytes]:exists`, `:[list[bytes]]:exists`, `:len>0`, `:json:exists`, `:string:exists`, `:in(data.asset,data.text)`, `=data.asset`} {
				sources = append(sources, value+suffix)
			}
			sources = append(sources, `[`+value+`,`+value+`]:distinct:size=1`)
		}
		sources = append(sources, receiver+`:get(key!):type="null"`, receiver+`[key!]=null`, receiver+`:get(key:[string]):type="null"`)
	}
	return append(sources,
		`["asset","text"]:map(key=>data:get(key):type):json:exists`,
		`["asset","text"]:map(k=>data[k]):map(v=>v:type):json:exists`,
		`["asset","text"]:map(k=>data[k]):filter(v=>v:exists):map(v=>v:type):json:exists`,
	)
}

func dynamicStructOptions(format hffsql.JSONFormat, metadata bool) []hffsql.Option {
	var settings []hffsql.JSONBindingOption
	if format == hffsql.JSONB && metadata {
		settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("records", "data_meta")))
	}
	return []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format, settings...), hffsql.BindJSON(hff.Path{"key"}, hffsql.Column("records", "key"), hffsql.JSON))}
}

func TestDynamicStructAccessBuild(t *testing.T) {
	e, err := engine.New(engine.WithSchema(dynamicStructSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range dynamicStructSources() {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			q, err := hffsql.Build(context.Background(), p, dynamicStructOptions(format, true)...)
			if err != nil {
				t.Error(format, source, err)
				continue
			}
			used := map[int]bool{}
			for _, match := range regexp.MustCompile(`\$(\d+)`).FindAllStringSubmatch(q.Where(), -1) {
				index, _ := strconv.Atoi(match[1])
				used[index] = true
			}
			for i := range q.Args() {
				if !used[i+1] {
					t.Errorf("%s %s argument %d unused", format, source, i+1)
				}
			}
		}
	}
}

func TestDynamicStructKeyLoweredOnce(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.struct-key.v1", Name: "structkey", Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(schema.Bytes("asset"), schema.String("text")))), schema.String("key"))), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))), engine.WithMethods(d))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:get(key:structkey):type="bytes"`, `data[key:structkey]:type="bytes"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSON), hffsql.Bind(hff.Path{"key"}, hffsql.Column("key"))), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, receiver hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
			return c.Expression(schema.StringType, "hff_key_marker({})", receiver)
		})))
		if err != nil {
			t.Fatal(err)
		}
		if count := strings.Count(q.Where(), "hff_key_marker("); count != 1 {
			t.Fatalf("key appears %d times: %s", count, q.Where())
		}
	}
}

func TestDynamicStructCancellation(t *testing.T) {
	var fields []hff.Field
	for i := 0; i < 24; i++ {
		fields = append(fields, schema.Field(fmt.Sprintf("f%d", i), schema.Struct(schema.String(fmt.Sprintf("v%d", i)))))
	}
	fields = append(fields, schema.Field("asset", schema.Struct(schema.Bytes("v0"))))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(fields...))), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`data:get(key):type="object"`, `data[key!]:type="object"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSON), hffsql.Bind(hff.Path{"key"}, hffsql.Column("key")))}
		baseline := &buildCancellationContext{Context: context.Background()}
		if _, err := hffsql.Build(baseline, p, options...); err != nil {
			t.Fatal(err)
		}
		for _, after := range []int64{1, baseline.checks.Load() / 4, baseline.checks.Load() / 2, baseline.checks.Load() - 1} {
			ctx, cancel := context.WithCancel(context.Background())
			q, err := hffsql.Build(&buildCancellationContext{Context: ctx, after: after, cancel: cancel}, p, options...)
			cancel()
			requireCode(t, err, hff.CancelledError)
			if q != nil || !errors.Is(err, context.Canceled) {
				t.Fatalf("partial %v, error %v", q, err)
			}
		}
	}
}

func TestDynamicStructFloatProvenance(t *testing.T) {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("data", schema.Struct(schema.Nullable(schema.Float("rate")), schema.String("text"), schema.Bytes("asset")))), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		source        string
		needsMetadata bool
	}{{`data:get(key):type="number"`, false}, {`data[key]:[float]>0`, false}, {`data:get(key):json:exists`, true}, {`data[key]:string:exists`, true}} {
		p, err := e.Compile(context.Background(), test.source)
		if err != nil {
			t.Fatal(err)
		}
		for _, metadata := range []bool{false, true} {
			var settings []hffsql.JSONBindingOption
			if metadata {
				settings = append(settings, hffsql.WithTypeMetadata(hffsql.Column("meta")))
			}
			_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSONB, settings...), hffsql.Bind(hff.Path{"key"}, hffsql.Column("key"))))
			if test.needsMetadata && !metadata {
				requireCode(t, err, hff.BackendError)
			} else if err != nil {
				t.Error(test.source, metadata, err)
			}
		}
	}
}

func TestDynamicStructRejectsAmbiguousStoredSchemas(t *testing.T) {
	for _, typ := range []hff.Type{schema.MapOf(schema.Union(schema.StringType, schema.BytesType)), schema.Struct(schema.Field("bad", schema.Union(schema.StringType, schema.BytesType)), schema.String("text")), schema.Union(schema.Struct(schema.String("value")), schema.Struct(schema.Bytes("value")))} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ), schema.String("key"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range []string{`data:get(key):type="bytes"`, `data[key]:json:exists`} {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(err)
			}
			_, err = hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSON), hffsql.Bind(hff.Path{"key"}, hffsql.Column("key"))))
			requireCode(t, err, hff.BackendError)
		}
	}
}
