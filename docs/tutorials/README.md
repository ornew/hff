# Tutorials

These tutorials build an HFF filter into a small Go service, one step at a
time. They use Go 1.27.1 and only APIs available in this repository.

1. [Write your first filter](01-first-filter.md): configure a schema, compile a
   human-authored query, build a local filter, and match records.
2. [Use a filter in a service](02-service-integration.md): compile and build
   once, validate records at ingestion, reuse validated inputs, and handle
   rejected queries as configuration errors.

Run each complete example from the repository root:

```sh
go run ./examples/tutorial-first-filter
go run ./examples/tutorial-service
```

For API details, continue with the [Go guides](../guides/README.md); for
language behavior, see the [specification](../../spec/README.md).
