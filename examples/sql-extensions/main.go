package main

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	if err := run(); err != nil {
		fmt.Println("Error:", err)
	}
}

func run() error {
	ctx := context.Background()
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{
		ID: "service.withsuffix.v1", Name: "withsuffix",
		Receiver: schema.StringType, Result: schema.StringType, BaseCost: 1,
	})
	if err != nil {
		return err
	}
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.String("name"))),
		engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method(declaration.ID()))),
		engine.WithMethods(declaration),
	)
	if err != nil {
		return err
	}
	program, err := e.Compile(ctx, `name:withsuffix="Alice!"`)
	if err != nil {
		return err
	}
	translator := sql.Implement(declaration, sql.PostgreSQL,
		func(call *sql.Call, receiver sql.Expression, args []sql.Expression) (sql.Expression, error) {
			suffix, err := call.Parameter(hff.String("!"))
			if err != nil {
				return sql.Expression{}, err
			}
			return call.Expression(schema.StringType, "{} || {}", receiver, suffix)
		},
	)
	query, err := sql.Build(ctx, program,
		sql.WithDialect(sql.PostgreSQL),
		sql.WithBindings(sql.Bind(hff.Path{"name"}, sql.Column("records", "name"))),
		sql.WithMethods(translator),
	)
	if err != nil {
		return err
	}
	guarantees, err := sql.ExecutionGuarantees(sql.PostgreSQL)
	if err != nil {
		return err
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
	fmt.Println("PostgreSQL MaxSteps guarantee:", guarantees.MaxSteps)
	return nil
}
