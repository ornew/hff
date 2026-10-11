# Go Guides

Use these guides to integrate HFF's Go packages into a service. Develop with
Go 1.27.1. The [language specification](../../spec/README.md) describes query
semantics; the [Go API record](../design/004-go-api.md) describes the complete
compilation and backend lifecycle.

- [Parse HFF syntax during development](parsing.md)
- [Inspect the compiled program](program-ir.md)
- [Configure an engine and validate records](getting-started.md)
- [Schemas and immutable values](schemas-and-values.md)
- [Capabilities and resource limits](configuration.md)

The current implementation provides values, schemas, feature selection, engine
configuration, validated input, and an internal syntax-only parser. Public query
compilation, local matching, SQL translation, and extension implementation
registration are subsequent stages.
