package sql_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func nativeColumnEngine(t testing.TB, mode string) (*engine.Engine, []hffsql.Binding) {
	t.Helper()
	fields := []schema.FieldDef{
		schema.Int("i"), schema.Int("j"), schema.Float("f"), schema.Float("g"),
		schema.String("s"), schema.String("t"), schema.Bool("p"), schema.Bool("q"),
		schema.Bytes("b"), schema.Bytes("c"), schema.Duration("d"), schema.Duration("e"),
	}
	var mappings []hffsql.Binding
	for index, field := range fields {
		if mode == "nullable" {
			fields[index] = schema.Nullable(field)
		} else if mode == "optional" {
			fields[index] = schema.Optional(field)
		}
		mappings = append(mappings, hffsql.Bind(hff.Path{field.Name}, hffsql.Column("records", field.Name)))
	}
	fields = append(fields, schema.List("items", schema.IntType), schema.Map("data", schema.AnyType))
	mappings = append(mappings, hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON), hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSON))
	e, err := engine.New(engine.WithSchema(schema.Struct(fields...)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e, mappings
}

func TestNativeColumnFiltersAvoidProjections(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"required", "nullable", "optional"} {
		e, mappings := nativeColumnEngine(t, mode)
		for _, source := range []string{
			`i:exists`, `!i:exists`, `f:exists`, `s:exists`, `p:exists`, `b:exists`, `d:exists`,
			`i=j`, `i!=j`, `i<j`, `!(i<j)`, `f>=g`, `s<t`, `p=q`, `b!=c`, `d>=e`,
			`b:len>0`, `b:len=0`, `!b:len<2`, `i:round=j`, `i:ceil<j`, `i:floor:round>1`,
			`d:nanoseconds=i`, `d:nanoseconds>0`, `d:nanoseconds:floor:exists`,
			`i+0=j*1`, `i:exists b:len>0 d:nanoseconds=i`,
		} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
			if err != nil {
				t.Fatal(source, err)
			}
			if strings.Contains(q.Where(), "SELECT") || strings.Contains(q.Where(), "OFFSET 0") {
				t.Errorf("%s %s retained projection barriers: %s", mode, source, q.Where())
			}
			if source == `i:exists` && mode == "required" && (q.Where() != "TRUE" || len(q.Args()) != 0) {
				t.Fatal("required native value existence was not folded under its schema contract", q.Where(), q.Args())
			}
		}
		for _, source := range []string{`i!:exists`, `i=f`, `f:round>1`, `data.i:exists`, `b!:len>0`, `items:any(x=>i:exists)`, `items:any(x=>x=i)`} {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
			if err != nil {
				t.Fatal(source, err)
			}
			if !strings.Contains(q.Where(), "SELECT") {
				t.Errorf("%s lost its generic value/presence path", source)
			}
		}
	}
}

func TestNativeColumnFiltersKeepLiteralArgumentOrder(t *testing.T) {
	ctx := context.Background()
	e, mappings := nativeColumnEngine(t, "nullable")
	p, err := e.Compile(ctx, `i>1 i=j b:len>2 s="end" d:nanoseconds>3`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(q.Args(), []any{int64(1), int64(2), "end", int64(3)}) {
		t.Fatal("column predicates changed surrounding argument order", q.Args())
	}
}

func TestNativeColumnFiltersRetainStructuredParentState(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(schema.Int("age"))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`profile.age:exists`, `profile!.age:exists`, `profile.age=profile.age`, `profile.age:round>0`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindStruct(hff.Path{"profile"}, hffsql.Boolean(true), hffsql.Boolean(false), hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "profile_age")))))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "SELECT") {
			t.Fatal("struct parent state was bypassed", source, q.Where())
		}
	}
}
