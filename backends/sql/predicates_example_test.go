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

func ExampleEqual() {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Optional(schema.Field("profile", schema.Struct(schema.String("name")))),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	program, err := e.Compile(ctx, `profile.name="Alice"`)
	if err != nil {
		panic(err)
	}
	// ColumnValue describes a physical column; Literal becomes a driver argument.
	present := hffsql.Equal(
		hffsql.ColumnValue(hffsql.Column("records", "tenant_id"), schema.StringType),
		hffsql.Literal(hff.String("tenant-17")),
	)
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindStruct(hff.Path{"profile"}, present, hffsql.Boolean(false),
			hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "profile_name")),
		),
	))
	if err != nil {
		panic(err)
	}
	// Pass these arguments separately when executing a statement using Where().
	fmt.Println(query.Args())
	// Output: [tenant-17 Alice]
}
