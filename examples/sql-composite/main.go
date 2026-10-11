package main

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	person := schema.Struct(schema.Nullable(schema.String("name")))
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Field("profile", schema.Struct(
			schema.Int("age"),
			schema.List("people", schema.NullableType(person)),
		))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	program, err := e.Compile(ctx, `profile!.age>=18 profile.people:any(x=>x!.name="Alice")`)
	if err != nil {
		panic(err)
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))),
	)
	if err != nil {
		panic(err)
	}
	fmt.Println("Predicate built:", query.Where() != "")
	fmt.Println("Parameter count:", len(query.Args()))
}
