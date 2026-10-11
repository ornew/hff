# Go Guides

Use these guides to integrate HFF's Go packages into a service. Develop with
Go 1.27.1. The [language specification](../../spec/README.md) describes query
semantics; the [Go API record](../design/004-go-api.md) describes the complete
compilation and backend lifecycle.

- [Inspect the compiled program](program-ir.md)
- [Configure an engine and validate records](getting-started.md)
- [Compile a query](compiling.md)
- [Build and run a local filter](local-evaluation.md)
- [Scalar and string methods](scalar-methods.md)
- [List and object methods](collections.md)
- [Schemas and immutable values](schemas-and-values.md)
- [Capabilities and resource limits](configuration.md)

The current implementation provides values, schemas, feature selection, engine
configuration, validated input, an internal syntax-only parser, public common
query compilation, core local matching, scalar/string methods, and list/object
methods. Collection callbacks, additional temporal/JSON/base64 methods, SQL
translation, and extension implementation registration remain unavailable.
Parser generation and the internal syntax parser are documented in the
[Development section](../development/README.md), separately from integration
guides.
