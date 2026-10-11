# Go Guides

Use these guides to integrate HFF's Go packages into a service. Develop with
Go 1.27.1. The [language specification](../../spec/README.md) describes query
semantics; the [Go API record](../design/004-go-api.md) describes the complete
compilation and backend lifecycle.

- [Parse HFF syntax during development](parsing.md)
- [Inspect the compiled program](program-ir.md)
- [Configure an engine and validate records](getting-started.md)
- [Compile a query](compiling.md)
- [Build and run a local filter](local-evaluation.md)
- [Scalar and string methods](scalar-methods.md)
- [Schemas and immutable values](schemas-and-values.md)
- [Capabilities and resource limits](configuration.md)

The current implementation provides values, schemas, feature selection, engine
configuration, validated input, an internal syntax-only parser, public common
query compilation, core local matching, and scalar/string methods including
byte length. List/object
methods, collection callbacks, additional temporal/JSON/base64 methods, SQL
translation, and extension implementation registration remain unavailable.
