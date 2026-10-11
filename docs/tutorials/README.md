# Introduction

These tutorials build an HFF filter into a small Go service, one step at a
time. They use Go 1.27.1 and only APIs available in this repository.
For a quick browser experiment, try the [Playground](../playground.md).

1. [Write your first filter](01-first-filter.md): configure a schema, compile a
   human-authored query, build a local filter, and match records.
2. [Use a filter in a service](02-service-integration.md): compile and build
   once, validate records at ingestion, reuse validated inputs, and handle
   rejected queries as configuration errors.
3. [Add a service-specific method](03-extension-methods.md): declare a typed
   signature, select its capability, and register a local evaluator.
4. [Translate a method to PostgreSQL](04-postgresql-translator.md): register a
   typed backend translator, build a parameterized predicate, and inspect
   execution guarantees without opening a database connection.

Run each complete example from the repository root:

```sh
go run ./examples/tutorial-first-filter
go run ./examples/tutorial-service
go run ./examples/extensions
go run ./examples/sql-extensions
```

For feature explanations, continue with the [guides](../guides/README.md).
Use the [cookbook](../cookbook/README.md) for focused filter recipes, the
[reference](../reference/README.md) for exact contracts, and
[Backends](../backends/README.md) for local and PostgreSQL behavior. The
[language specification](../../spec/README.md) is normative.
