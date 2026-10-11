package sql_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func sqlEngine(t *testing.T) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Int("age")), schema.Nullable(schema.String("name")),
		schema.Nullable(schema.Bool("flag")), schema.Nullable(schema.Float("factor")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func bindings() []hffsql.Binding {
	return []hffsql.Binding{
		hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")),
		hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")),
		hffsql.Bind(hff.Path{"flag"}, hffsql.Column("records", "flag")),
		hffsql.Bind(hff.Path{"factor"}, hffsql.Column("records", "factor")),
	}
}

func compile(t *testing.T, source string) hff.Program {
	t.Helper()
	p, err := sqlEngine(t).Compile(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestBuildConfigurationAndOwnership(t *testing.T) {
	ctx := context.Background()
	p := compile(t, `age>=18 name="O'Reilly"`)
	path := hff.Path{"age"}
	parts := []string{"records", "age"}
	binding := hffsql.Bind(path, hffsql.Column(parts...))
	path[0] = "bad"
	parts[1] = "bad"
	config := &hffsql.Config{Dialect: hffsql.PostgreSQL, Bindings: bindings()}
	query, err := hffsql.Build(ctx, p, hffsql.WithConfig(config), hffsql.WithBindings(append([]hffsql.Binding{binding}, bindings()[1:]...)...))
	if err != nil {
		t.Fatal(err)
	}
	config.Dialect = "unsupported"
	config.Bindings[0] = hffsql.Binding{}
	if !strings.Contains(query.Where(), `"records"."age"`) || strings.Contains(query.Where(), "O'Reilly") {
		t.Fatal("identifiers must be quoted; literals must be bound:", query.Where())
	}
	if !reflect.DeepEqual(query.Args(), []any{int64(18), "O'Reilly"}) {
		t.Fatal(query.Args())
	}
	args := query.Args()
	args[0] = int64(99)
	if query.Args()[0] != int64(18) {
		t.Fatal("Args exposes mutable storage")
	}
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 100 {
				if query.Args()[0] != int64(18) || query.Where() == "" {
					t.Error("shared Query mutated")
				}
			}
		})
	}
	workers.Wait()
	for _, options := range [][]hffsql.Option{
		nil, {hffsql.WithDialect("unknown")}, {hffsql.Option{}}, {hffsql.WithConfig(nil)},
		{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Binding{})},
		{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()[0], bindings()[0])},
		{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"unknown"}, hffsql.Column("x")))},
		{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("x")), hffsql.Bind(hff.Path{"name"}, hffsql.Column("x")))},
	} {
		_, err := hffsql.Build(ctx, p, options...)
		requireCode(t, err, hff.ConfigurationError)
	}
	_, err = hffsql.Build(nil, p, hffsql.WithDialect(hffsql.PostgreSQL))
	requireCode(t, err, hff.ConfigurationError)
	_, err = hffsql.Build(ctx, hff.Program{}, hffsql.WithDialect(hffsql.PostgreSQL))
	requireCode(t, err, hff.ConfigurationError)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = hffsql.Build(cancelled, p, hffsql.WithDialect(hffsql.PostgreSQL))
	requireCode(t, err, hff.CancelledError)
	if !errors.Is(err, context.Canceled) {
		t.Fatal("lost cancellation cause")
	}
}

func requireCode(t *testing.T, err error, code hff.ErrorCode) *hff.Error {
	t.Helper()
	var detail *hff.Error
	if !errors.As(err, &detail) || detail.Code != code {
		t.Fatalf("got %v; want %s", err, code)
	}
	return detail
}

func TestIdentifiersAndLiteralPaths(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("service.name"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, "`service.name`=\"auth\"")
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"service.name"}, hffsql.Column("app.schema", `odd"name`))))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query.Where(), `"app.schema"."odd""name"`) {
		t.Fatal(query.Where())
	}
	for _, parts := range [][]string{nil, {""}, {strings.Repeat("x", 64)}, {"has\x00nul"}, {string([]byte{0xff})}} {
		_, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"service.name"}, hffsql.Column(parts...))))
		requireCode(t, err, hff.ConfigurationError)
	}
}

func TestUnsupportedSQLHasSourceLocation(t *testing.T) {
	for _, source := range []string{`age+1>2`, `age>factor`, `name~"a"`, `name:lower="a"`, `name:upper="A"`, `name="\u0000"`} {
		p := compile(t, source)
		_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		detail := requireCode(t, err, hff.BackendError)
		if detail.Span == nil || detail.Line < 1 || detail.Column < 1 {
			t.Fatal("missing source location:", err)
		}
	}
	p := compile(t, "flag |\n name=\"a\"")
	_, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()[2]))
	detail := requireCode(t, err, hff.BackendError)
	if detail.Line != 2 || detail.Column != 2 {
		t.Fatal("wrong unbound field location:", err)
	}
}

func TestRequiredNestedPathsAndUnrepresentableParents(t *testing.T) {
	ctx := context.Background()
	user := schema.Field("user", schema.Struct(schema.Optional(schema.String("name"))))
	for i, field := range []schema.FieldDef{user, schema.Optional(user), schema.Nullable(user)} {
		e, err := engine.New(engine.WithSchema(schema.Struct(field)), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		p, err := e.Compile(ctx, `user.name="Alice"`)
		if err != nil {
			t.Fatal(err)
		}
		options := []hffsql.Option{hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"user", "name"}, hffsql.Column("name")))}
		_, err = hffsql.Build(ctx, p, options...)
		if i > 0 {
			requireCode(t, err, hff.ConfigurationError)
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		guarded, err := e.Compile(ctx, `user!.name="Alice"`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = hffsql.Build(ctx, guarded, options...)
		requireCode(t, err, hff.BackendError)
	}
}

func TestInternalAliasesDoNotShadowColumnNames(t *testing.T) {
	p := compile(t, `age>1`)
	query, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("__hff_1"))))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(query.Where(), `AS "__hff_1"`) {
		t.Fatal("internal alias shadows bound column")
	}
}

type parityCase struct {
	source                  string
	age, name, flag, factor hff.Value
}

func parityCases() []parityCase {
	var cases []parityCase
	queries := []string{
		``, `age`, `name`, `flag`, `factor`,
		`age>=18 name:contains("A") | flag`,
		`age=null`, `age!=null`, `age=null | flag`,
		`!name="Alice"`, `name!="Alice"`, `!(name="Alice")`,
		`age!=age`, `age=age`, `age>age`,
		`age!=-1`, `-age>1`, `factor>-0.0`,
		`age=-9223372036854775808`, `age=9223372036854775807`, `age>9223372036854775806`,
		`factor=1.7976931348623157e308`, `factor=5e-324`, `factor=-0.0`,
		`age!=null age>1`, `age!=null | !flag`,
		`age!`, `age!:exists`, `age!=null age!:exists`,
		`(age!)=null`, `!(age!)=null`, `(age!)!=null`,
		`name:[string]="Alice"`, `name:[null]:exists`, `name:[string|null]:exists`,
		`name:[string]:len=5`, `name:[string]:type="string"`,
		`name:[int]>1`, `name:[int|null]:type="null"`,
		`age:[float]:exists`, `age:[number]:exists`,
		`name:type="null"`, `age:type="number"`, `factor:type="number"`,
		`name:contains("A",null):type="null"`,
		`name:contains("A",name!):type="null"`,
		`age!=null | !(age!=null)`, `age!=null | age!=null`,
		`age!=null age!=null`, `!(age!=null)`,
		`age!=null | !(age!=null age!=null)`,
		`age!=null | !(age!=null | age!=null)`,
		`age!=null age!>1`, `!(age!=null age!>1)`,
		`name:len=5`, `name:len=2`, `name:contains("A","i")`,
		`name:contains("A",null):exists`,
		`name:startswith("A")`, `name:endswith("e")`, `name:startswith("")`,
		`name:concat("!")="Alice!"`, `name:concat(null):exists`,
		`name:concat(age:[string]):type="null"`,
		`name:contains(age:[string]):type="null"`,
		`name<"é"`, `name>"A"`, `name="O'Reilly"`,
		`name="é" | name="é"`,
		`flag:not`, `flag:and(true)`, `flag:or(false)`,
		`flag:and(flag!):exists`, `flag:or(flag!):exists`,
		`flag:and(null):exists`, `flag:or(null):exists`,
		`flag:and(null):type="null"`, `flag:or(null):type="null"`,
		`flag:and(age:[bool]):exists`, `flag:or(age:[bool]):exists`,
		`flag:and(age:[bool]):type="bool"`, `flag:or(age:[bool]):type="bool"`,
	}
	queries = append(queries, "flag"+strings.Repeat(":and(true)", 20))
	rows := [][4]hff.Value{
		{hff.Null(), hff.Null(), hff.Null(), hff.Null()},
		{hff.Int(18), hff.String("Alice"), hff.Bool(true), hff.Float(1.25)},
		{hff.Int(0), hff.String(""), hff.Bool(false), hff.Float(math.Copysign(0, -1))},
		{hff.Int(-2), hff.String("O'Reilly"), hff.Bool(false), hff.Float(-1.25)},
		{hff.Int(math.MinInt64), hff.String("é"), hff.Bool(true), hff.Float(math.SmallestNonzeroFloat64)},
		{hff.Int(math.MaxInt64), hff.String("é"), hff.Bool(false), hff.Float(math.MaxFloat64)},
	}
	for _, source := range queries {
		for _, row := range rows {
			cases = append(cases, parityCase{source, row[0], row[1], row[2], row[3]})
		}
	}
	return cases
}

func TestChainedSQLHasBoundedExpansion(t *testing.T) {
	ctx := context.Background()
	p := compile(t, "flag"+strings.Repeat(":and(true)", 40))
	query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Where()) > 64*1024 {
		t.Fatal("chained calls expand child SQL recursively:", len(query.Where()))
	}
	if len(query.Args()) != 40 {
		t.Fatal("lost or duplicated parameter allocation")
	}
}

func TestScalarQueriesBuildAndLocalReference(t *testing.T) {
	e := sqlEngine(t)
	ctx := context.Background()
	for _, tc := range parityCases() {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		if query.Where() == "" {
			t.Fatal("empty predicate")
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		_, err = filter.Match(ctx, hff.Object(map[string]hff.Value{"age": tc.age, "name": tc.name, "flag": tc.flag, "factor": tc.factor}))
		if err != nil {
			t.Fatal(tc.source, err)
		}
	}
}
