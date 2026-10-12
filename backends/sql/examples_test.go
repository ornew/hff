package sql_test

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func ExampleBuild() {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("age"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	program, err := e.Compile(ctx, `age>=18`)
	if err != nil {
		panic(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("people", "age"))),
	)
	if err != nil {
		panic(err)
	}
	// Supply this statement and Args to your driver's QueryContext. Build never
	// connects to the database, and Where is only the predicate fragment.
	statement := `SELECT "people"."id" FROM "people" WHERE ` + query.Where()
	fmt.Println("statement built:", statement != "")
	fmt.Println("bound values:", query.Args())
	// Output:
	// statement built: true
	// bound values: [18]
}

func ExampleNewRelation() {
	// HFF sees items as a list of objects. SQL stores one item per child row.
	itemType := schema.Struct(schema.Int("price"))
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(itemType,
			hffsql.BindStruct(hff.Path{}, hffsql.Boolean(true), hffsql.Boolean(false),
				hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")))),
		// The application guarantees that this order tuple is unique.
		hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("item", "id"), schema.IntType)),
	)
	if err != nil {
		panic(err)
	}
	// Pass the root-path binding to WithBindings when building the Program.
	binding := hffsql.BindRelation(hff.Path{"items"}, relation)
	_ = binding
	fmt.Println("relation configured without DB access")
	// Output: relation configured without DB access
}

func ExampleCall_Expression() {
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
		ID: "example.emphasis", Name: "emphasis", Receiver: schema.StringType,
		Result: schema.StringType, BaseCost: 1,
	})
	if err != nil {
		panic(err)
	}
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.String("name"))),
		engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))),
		engine.WithMethods(declaration),
	)
	if err != nil {
		panic(err)
	}
	ctx := context.Background()
	program, err := e.Compile(ctx, `name:emphasis="Alice!"`)
	if err != nil {
		panic(err)
	}
	implementation := hffsql.Implement(declaration, hffsql.PostgreSQL,
		func(call *hffsql.Call, receiver hffsql.Expression, args []hffsql.Expression) (hffsql.Expression, error) {
			suffix, err := call.Parameter(hff.String("!"))
			if err != nil {
				return hffsql.Expression{}, err
			}
			// {} inserts scoped expressions; values are driver parameters. The
			// developer owns the SQL template's types, meaning and purity.
			return call.Expression(schema.StringType, "({} || {})", receiver, suffix)
		})
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("people", "name"))),
		hffsql.WithMethods(implementation),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println(query.Args())
	// Output: [! Alice!]
}
