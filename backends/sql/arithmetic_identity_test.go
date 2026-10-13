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

func identityEngine(t *testing.T) (*engine.Engine, []hffsql.Option) {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Int("age")), schema.Nullable(schema.Float("ratio")),
		schema.Any("value"), schema.List("items", schema.NullableType(schema.FloatType)), schema.String("name"),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e, []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
		hffsql.Bind(hff.Path{"ratio"}, hffsql.Column("records", "ratio")),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
		hffsql.BindJSON(hff.Path{"value"}, hffsql.Column("records", "value"), hffsql.JSON),
		hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON),
	)}
}

func TestArithmeticIdentitiesKeepNativePredicatesDirect(t *testing.T) {
	e, options := identityEngine(t)
	for _, source := range []string{
		`age+0>18`, `(0+age)>18`, `age-0>18`, `age*1>18`, `(1*age)>18`,
		`age+0.0>18.0`, `(0.0+age)>18.0`, `age-0.0>18.0`,
		`age*1.0>18.0`, `(1.0*age)>18.0`, `age/1>18.0`, `age/1.0>18.0`,
		`ratio*1>18.0`, `(1*ratio)>18.0`, `ratio*1.0>18.0`, `(1.0*ratio)>18.0`,
		`ratio/1>18.0`, `ratio/1.0>18.0`, `ratio-0>18.0`, `ratio-0.0>18.0`,
		`age*"1":number>18`, `age*1*1>18`, `age/1/1>18.0`,
		`ratio*1.0/1.0-0.0>18.0`,
	} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, options...)
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(q.Where(), "SELECT") || strings.Contains(q.Where(), "OFFSET 0") {
			t.Errorf("%s retained arithmetic/projection work: %s", source, q.Where())
		}
		if len(q.Args()) != 1 {
			t.Errorf("%s retained removed numeric operand: %#v", source, q.Args())
		}
		if strings.Contains(source, "age") && strings.Contains(source, "18.0") && !strings.Contains(q.Where(), "::double precision") {
			t.Errorf("%s lost required Int promotion: %s", source, q.Where())
		}
	}
}

func TestArithmeticIdentitiesLeaveSignedZeroAndDynamicFamilies(t *testing.T) {
	e, options := identityEngine(t)
	for _, source := range []string{`ratio+0.0=0.0`, `ratio+0=0.0`, `(0.0+ratio)=0.0`, `ratio-(-0.0)=0.0`, `value*1=0`, `age%1=0`, `age*0=0`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(context.Background(), p, options...)
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(q.Where(), "SELECT") || len(q.Args()) < 2 {
			t.Errorf("%s incorrectly removed nonidentity arithmetic: %s %#v", source, q.Where(), q.Args())
		}
	}
}

func TestArithmeticIdentityPreservesExtensionAndArgumentOrder(t *testing.T) {
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "test.intvalue.v1", Name: "intvalue", Receiver: schema.StringType, Parameters: []hff.Type{schema.IntType}, Result: schema.IntType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("age"), schema.String("name"))), engine.WithMethods(d), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(d.ID()))))
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{`age>(1*name:intvalue(7)) name="tail"`, `age>(name:intvalue(7)*1) name="tail"`} {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(source, err)
		}
		calls := 0
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))),
			hffsql.WithMethods(hffsql.Implement(d, hffsql.PostgreSQL, func(c *hffsql.Call, _ hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
				calls++
				return c.Expression(schema.IntType, "{}", args[0])
			})))
		if err != nil || calls != 1 {
			t.Fatalf("%s: calls=%d err=%v", source, calls, err)
		}
		if !reflect.DeepEqual(q.Args(), []any{int64(7), "tail"}) {
			t.Errorf("%s changed source argument order: %#v", source, q.Args())
		}
	}
}
