package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestDirectFloatPredicatesPreserveNumericSubtype(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Float("factor")), schema.Nullable(schema.Int("age")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.Bind(hff.Path{"factor"}, hffsql.Column("records", "factor")),
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
	}
	for _, source := range []string{
		`factor=1.5`, `factor!=1.5`, `factor<1.5`, `factor<=1.5`,
		`factor>1.5`, `factor>=1.5`, `factor=-0.0`, `factor=5e-324`,
		`factor`, `!factor`, `factor=null`, `factor!=null`,
		`!factor<1.5`, `age>18 factor<1.5`, `age>18|factor<1.5`,
		`factor:in(0.0,-0.0,1.5,null)`,
		`factor=1`, `factor=9007199254740993`, `factor>9223372036854775807`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(query.Where(), "SELECT") || strings.Contains(query.Where(), "OFFSET 0") {
			t.Fatalf("%s retained projection barriers: %s", source, query.Where())
		}
		for _, argument := range query.Args() {
			if _, floating := argument.(float64); !floating && source != `age>18 factor<1.5` && source != `age>18|factor<1.5` {
				t.Fatalf("%s lost float parameter subtype: %T", source, argument)
			}
		}
	}
	for _, source := range []string{
		`factor=age`, `factor:in(1)`, `factor+1.0>2.0`, `factor!>1.5`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if !strings.Contains(query.Where(), "SELECT") {
			t.Fatalf("%s bypassed exact mixed/computed lowering: %s", source, query.Where())
		}
	}
}
