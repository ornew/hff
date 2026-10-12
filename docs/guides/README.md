# Introduction

These pages explain how HFF query features behave and how to use them from Go.
For a staged service walkthrough, start with the
[tutorials](../tutorials/README.md). For use-case recipes, see the
[cookbook](../cookbook/README.md). Backend-specific configuration and support
are documented under [Backends](../backends/README.md), and exact signatures
and API contracts are collected in the [Reference](../reference/README.md).

- [Configure an engine and validate records](getting-started.md)
- [Compile a query](compiling.md)
- [Schemas and immutable values](schemas-and-values.md)
- [Converting Go values](go-values.md): explicitly map Go records to an HFF
  schema without changing HFF's value or type semantics.
- [Scalar and string methods](scalar-methods.md)
- [List and object methods](collections.md)
- [List callbacks and transformations](callbacks.md)
- [JSON parsing and serialization](json.md)
- [Timestamps, durations, and time zones](temporal.md)

## Implementation status

The local backend supports the documented common operations, built-in method
families, callbacks, JSON and temporal conversions, and local extension
evaluators. PostgreSQL support and its current limits are listed in the
[PostgreSQL backend reference](../backends/postgresql/README.md). A successful
compile does not imply that every backend can build the program.
