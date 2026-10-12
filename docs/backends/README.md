# Introduction

HFF keeps query syntax and common compilation separate from backend building.
Compile checks language semantics and selected capabilities; a backend then
checks whether it can implement the compiled operations. Unsupported behavior
must fail explicitly rather than being silently omitted.

- [Local backend](local/README.md): build reusable filters, match validated
  values, configure local limits, and register local extension evaluators.
- [PostgreSQL backend](postgresql/README.md): build parameterized predicates,
  inspect backend guarantees, and register PostgreSQL method translators.
  Start with the [PostgreSQL binding tutorial](../tutorials/06-postgresql-bindings.md)
  to connect a logical HFF record to relational rows.
  PostgreSQL-specific guides cover [JSON/JSONB](postgresql/json.md) and
  [native composite bindings](postgresql/composites.md),
  [flattened structs](postgresql/flattened-structures.md), and
  [correlated relations](postgresql/relations.md).

These builders do not own database execution. The application owns connections,
transactions, and database timeouts. Consult the backend support matrices before
relying on a particular operation.
