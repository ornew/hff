package sql_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestBytesProfileAndJSONMaterialization(t *testing.T) {
	profile, err := hffsql.Profile(hffsql.PostgreSQL)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bytes.len", "bytes.base64", "string.frombase64"} {
		feature, ok := profile.Feature(capabilities.Method(id))
		if !ok || feature.Level != hff.Conditional {
			t.Fatalf("%s: %+v", id, feature)
		}
	}
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Bytes("data"), schema.List("items", schema.StringType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{hffsql.Bind(hff.Path{"data"}, hffsql.Column("data")), hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("items"), hffsql.JSON)}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		p, err := e.Compile(ctx, `data:base64`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), format)))
		if err != nil {
			t.Fatalf("%s rejected schema-known JSON Bytes: %v", format, err)
		}
	}
	for _, source := range []string{`data:base64`, `data:len>0`, `data:string`, `data:json`, `data:base64:frombase64=data`, `data:[bytes]:exists`, `data:exists=([null:frombase64]:json="[null]")`, `data:exists=[(null!):frombase64]:exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		if _, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...)); err != nil {
			t.Fatal(source, err)
		}
	}
	for _, source := range []string{`[data]:json`, `{"value":data}:json`, `items:map(x=>x:frombase64):json`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatalf("%s rejected Bytes construction: %v", source, err)
		}
	}
}

func TestRelationOrderRejectsBytes(t *testing.T) {
	_, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("children"), "child"),
		hffsql.WithCorrelation(hffsql.TrustedSQL("true")),
		hffsql.WithRelationItem(schema.BytesType, hffsql.Bind(hff.Path{}, hffsql.Column("child", "data"))),
		hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("child", "data"), schema.BytesType)),
	)
	if err == nil || !strings.Contains(err.Error(), "relation order requires") {
		t.Fatalf("bytes widened relation ordering: %v", err)
	}
}

func TestBytesExtensionParametersStayImmutable(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "service.bytes.v1", Name: "rawbytes", Receiver: schema.StringType, Result: schema.BytesType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"))), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	p, err := e.Compile(ctx, `name:rawbytes:base64="AP8="`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("name"))), hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, _ []hffsql.Expression) (hffsql.Expression, error) {
		return c.Parameter(hff.Bytes([]byte{0, 255}))
	})))
	if err != nil {
		t.Fatal(err)
	}
	args := q.Args()
	if len(args) != 2 || args[0] != "00ff" || !strings.Contains(q.Where(), "decode($1::text,'hex')") {
		t.Fatalf("SQL=%s args=%v", q.Where(), args)
	}
	args[0] = "changed"
	if q.Args()[0] != "00ff" {
		t.Fatal("argument slice mutation changed the query")
	}
}

func TestJSONBytesRejectsUnrecoverableIdentity(t *testing.T) {
	ctx := context.Background()
	for _, test := range []struct {
		typ     hff.Type
		sources []string
	}{
		{schema.ListOf(schema.BytesType), []string{`data=other`, `other=data`, `other:contains(data)`, `data:in(other)`, `[data[0],"AA=="]:size>0`}},
		{schema.ListOf(schema.Union(schema.Struct(schema.Bytes("v")), schema.Struct(schema.String("v")))), []string{`data:[list[map[string]]]:exists`, `data:[list[map[bytes]]]:exists`}},
		{schema.ListOf(schema.Union(schema.BytesType, schema.StringType)), []string{`data:json`, `data:sort:json`, `data:unique`}},
		{schema.MapOf(schema.BytesType), []string{`data:items:json`}},
		{schema.MapOf(schema.Union(schema.BytesType, schema.StringType)), []string{`data:json`, `data:values:unique`}},
		{schema.Union(schema.BytesType, schema.StringType), []string{`data:json`, `data:[bytes]:exists`}},
	} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", test.typ), schema.Any("other"))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for _, source := range test.sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			_, err = hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("data"), hffsql.JSON), hffsql.BindJSON(hff.Path{"other"}, hffsql.Column("other"), hffsql.JSON)))
			var detail *hff.Error
			if !errors.As(err, &detail) || detail.Code != hff.BackendError {
				t.Fatalf("%s accepted unrecoverable Bytes identity: %v", source, err)
			}
		}
	}
}
