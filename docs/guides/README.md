# Go Guides

Use these guides to integrate HFF's Go packages into a service. Develop with
Go 1.27.1. The [language specification](../../spec/README.md) describes query
semantics; the [Go API record](../design/004-go-api.md) describes the complete
compilation and backend lifecycle.

For a staged introduction that builds and runs a complete local filter, start
with the [tutorials](../tutorials/README.md). These guides provide focused API
reference and operational detail.

- [Inspect the compiled program](program-ir.md)
- [Configure an engine and validate records](getting-started.md)
- [Compile a query](compiling.md)
- [Build and run a local filter](local-evaluation.md)
- [Scalar and string methods](scalar-methods.md)
- [Timestamps, durations, and time zones](temporal.md)
- [List and object methods](collections.md)
- [List callbacks and transformations](callbacks.md)
- [JSON parsing and serialization](json.md)
- [Declare and implement extension methods](extensions.md)
- [Build PostgreSQL predicates](sql.md)
- [Schemas and immutable values](schemas-and-values.md)
- [Capabilities and resource limits](configuration.md)

The current implementation provides values, schemas, feature selection, engine
configuration, validated input, an internal syntax-only parser, public common
query compilation, core local matching, scalar/string and list/object methods,
base64 and JSON conversions, temporal methods, collection callbacks, local
extension declarations/evaluators, and a PostgreSQL scalar predicate backend.
SQL extension translators, broader SQL semantics, and additional SQL dialects
remain unavailable.
Parser generation and the internal syntax parser are documented in the
[Development section](../development/README.md), separately from integration
guides.
