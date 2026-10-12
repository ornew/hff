package engine_test

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func ExampleNew() {
	base := &engine.Config{
		Schema:       schema.Struct(schema.Int("age")),
		Capabilities: capabilities.Standard(),
	}
	// Later options replace fields from the common configuration.
	e, err := engine.New(engine.WithConfig(base),
		engine.WithCapabilities(base.Capabilities.Without(capabilities.Regex)))
	if err != nil {
		panic(err)
	}
	fmt.Println("regex enabled:", e.Capabilities().Has(capabilities.RegexOperators))
	fmt.Println("base unchanged:", base.Capabilities.Has(capabilities.RegexOperators))
	// Output:
	// regex enabled: false
	// base unchanged: true
}

func ExampleEngine_Compile() {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"), schema.Int("age"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	program, err := e.Compile(context.Background(), `name="Alice" age>=18`)
	if err != nil {
		panic(err)
	}
	// A Program is a typed plan. A backend Build decides how it will execute.
	fmt.Println("valid plan:", program.Valid())
	fmt.Println("schema retained:", program.Schema().Equal(e.Schema()))
	// Output:
	// valid plan: true
	// schema retained: true
}

func ExampleEngine_Validate_missingField() {
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("age"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	_, err = e.Validate(context.Background(), hff.Object(nil))
	fmt.Println("missing required nonnullable age:", err != nil)
	// Output: missing required nonnullable age: true
}
