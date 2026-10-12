# Bind a Go Record Type

This tutorial derives an HFF schema from a Go type, compiles a filter against
that schema, and matches typed records without writing a reflection converter.

## Define the record and infer its schema

The example record uses `json` tags. A field can instead use an `hff` tag when
the HFF name should differ from its JSON name:

```go
type Person struct {
	Name string `json:"name"`
	Age  int    `json:"age"`
}

binding, err := govalue.NewBinding[Person]()
if err != nil {
	return err
}
```

The binding owns a snapshot of the inferred schema and conversion plan. Use
`binding.Schema()` when configuring the engine so compilation and record
conversion share the same type contract.

## Compile and build once

```go
e, err := engine.New(
	engine.WithSchema(binding.Schema()),
	engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
	return err
}
program, err := e.Compile(ctx, `age>=18 name`)
if err != nil {
	return err
}
filter, err := binding.Build(ctx, program)
if err != nil {
	return err
}
```

`Build` rejects a program compiled with a different schema. Then pass records
directly to the typed filter:

```go
matched, err := filter.Match(ctx, Person{Name: "Alice", Age: 24})
if err != nil {
	return err
}
fmt.Println(matched)
```

The binding converts and validates each record before local evaluation. The
source record must not be mutated during `Match`. Conversion preserves Go
numeric kinds, maps nil containers and pointers to null, and applies the input
depth/value limits. It rejects recursive types at binding construction.

For the complete runnable program, run this from the repository root:

```sh
go run ./examples/go-binding
```

The example prints the name of the one matching record. For explicit HFF
schemas and custom Go struct mappings, use the [Go value guide](../guides/go-values.md).
