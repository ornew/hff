package local_test

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
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
	filter, err := local.Build(ctx, program)
	if err != nil {
		panic(err)
	}
	for _, age := range []int64{16, 25} {
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"age": hff.Int(age)}))
		if err != nil {
			panic(err)
		}
		fmt.Println(age, matched)
	}
	// Output:
	// 16 false
	// 25 true
}

func ExampleFilter_MatchInput() {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("age"))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	record := hff.Object(map[string]hff.Value{"age": hff.Int(25)})
	input, err := e.Validate(ctx, record)
	if err != nil {
		panic(err)
	}
	// Validate the record once, then reuse it with programs of this exact schema.
	for _, source := range []string{`age>=18`, `age>=65`} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			panic(err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			panic(err)
		}
		matched, err := filter.MatchInput(ctx, input)
		if err != nil {
			panic(err)
		}
		fmt.Println(source, matched)
	}
	// Output:
	// age>=18 true
	// age>=65 false
}

func ExampleImplement() {
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
	implementation := local.Implement(declaration,
		func(call *local.Call, receiver hff.Value, args []hff.Value) (hff.Value, error) {
			text, _ := receiver.AsString() // Dispatch has checked the declared type.
			// Account for work proportional to the input and the produced value.
			if err := call.ChargeSteps(len(text)); err != nil {
				return hff.Value{}, err
			}
			if err := call.ChargeGeneratedValues(1); err != nil {
				return hff.Value{}, err
			}
			return hff.String(text + "!"), nil
		})
	filter, err := local.Build(ctx, program, local.WithMethods(implementation))
	if err != nil {
		panic(err)
	}
	matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"name": hff.String("Alice")}))
	if err != nil {
		panic(err)
	}
	fmt.Println(matched)
	// Output: true
}
