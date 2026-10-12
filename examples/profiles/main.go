package main

import (
	"context"
	"fmt"
	"log"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"))),
		engine.WithCapabilities(capabilities.Standard().Without(capabilities.Regex)))
	if err != nil {
		log.Fatal(err)
	}
	p, err := e.Compile(ctx, `name="Alice"`)
	if err != nil {
		log.Fatal(err)
	}
	f, err := local.Build(ctx, p, local.WithLimits(hff.Limits{MaxSteps: 1000}))
	if err != nil {
		log.Fatal(err)
	}
	profile := f.Profile()
	limits, known := profile.RuntimeLimits()
	fmt.Printf("%s: steps=%d, known=%t, regex allowed=%t\n", profile.Backend(), limits.MaxSteps, known,
		profile.Capabilities().Has(capabilities.Regex))
	feature, known := profile.Feature(capabilities.RegexOperators)
	fmt.Printf("regex implementation: level=%d, known=%t\n", feature.Level, known)

	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name"))))
	if err != nil {
		log.Fatal(err)
	}
	sqlProfile := q.Profile()
	_, known = sqlProfile.RuntimeLimits()
	fmt.Printf("%s: runtime limits known=%t, step guarantee=%t\n", sqlProfile.Backend(), known,
		sqlProfile.HasGuarantee(hff.StepBudgetGuarantee))
}
