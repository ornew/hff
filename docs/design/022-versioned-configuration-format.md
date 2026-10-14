# 022. Versioned configuration documents

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-14

## Summary

HFF needs a portable way for tools to accept a schema and backend bindings as
data, validate them, and build a backend artifact without executing a query.
Version one defines a strict JSON document with separate `engine` and
`backend` objects. The document describes configuration; it does not serialize
Go closures, compiled programs, database connections, or query results.

## Motivation

The public Go API separates engine configuration from backend construction.
An engine contains a schema, explicit capability IDs, limits, and extension
declarations. PostgreSQL Build receives a dialect, schema bindings, execution
limits, and SQL translator implementations. The configuration types are
constructed through APIs and contain private implementation fields; directly
encoding their Go values would not define a portable format. Several options
also hold closures, which cannot be reconstructed from JSON.

The browser SQL Playground needs a data form for a schema, bindings, and
PostgreSQL options. Its consumer must validate that form before compiling HFF
or building SQL. A successful build returns a `WHERE` expression and ordered
arguments; it does not connect to a database or execute the predicate.

## Goals

- Define a strict JSON envelope with an integer format version and separate
  engine and backend configuration.
- Preserve schema structure, explicit capability selection, limits, extension
  declarations, binding variants, and typed SQL predicate structure.
- Support native scalar, JSON/JSONB, native composite, timestamp, flattened
  struct, and correlated relation bindings.
- Require host registries for trusted SQL predicates and SQL extension
  translators. The document carries stable IDs, never executable code or raw
  SQL text; an unknown ID is a decoding error.
- Reject unknown versions, variants, fields, malformed types, invalid physical
  shapes, and configurations that cannot be represented faithfully.
- Keep decoding and Build free of database I/O.

## Non-goals

- Serializing a compiled `Program`, Go `Option` closures, SQL translator
  functions, or a live database connection.
- Allowing a document to supply arbitrary SQL text, Go source, or executable
  callbacks.
- Inferring table layouts, foreign keys, joins, or database column types.
- Promising that a syntactically valid configuration is supported by every
  backend operation; backend `Build` remains authoritative for a program.

## Design

### Document envelope

The version-one envelope has two top-level configuration scopes:

```json
{
  "version": 1,
  "engine": {
    "schema": {"kind": "struct", "fields": []},
    "capabilities": ["access:field", "operator:comparison"],
    "limits": {},
    "unlimitedLimits": false,
    "methods": []
  },
  "backend": {
    "kind": "postgresql",
    "bindings": [],
    "translatorIDs": [],
    "executionLimits": {}
  }
}
```

The PostgreSQL backend currently accepts only zero `executionLimits`, because
it cannot enforce HFF execution budgets. The engine `limits` object uses the
named `max...` fields exposed by the HFF configuration.

The decoder checks the version and each tagged variant, rejects unknown fields
and duplicate object keys, and validates the complete configuration before
returning it. Omitted defaults and explicit unlimited limits must remain
distinct. The encoded document represents the final configuration, not the
history or ordering of Go option calls.

### Schema descriptors

Schema types use recursive tagged descriptors for primitive values, lists,
maps, fixed-field structs, and unions. Lists and maps use an `element` type
descriptor. A struct field carries its exact name, type, and independent
optional flag. Nullability is represented by including `null` in the value
type; it is not inferred from `optional`. Input validation accepts an absent
nullable field even when `optional` is false. Union members must be valid,
normalized, and distinct. Function types are not valid input schema types.

SQL scalar literals use a `kind` and canonical `text` pair. Int and Duration
use canonical base-10 integer text; finite Float uses Go's round-trip decimal
spelling; Bool uses `true` or `false`; String uses its value as text; Null has
no text. String predicate literals must be valid UTF-8 and contain no NUL;
`Encode` and `Decode` reject values outside that constraint. This avoids loss of
`int64` precision in JavaScript environments while preserving the declared HFF
kind.

### Binding variants

Every binding has a tagged kind, an HFF path represented as an array of literal
path components, and the physical details required by its constructor:

- **Native column** stores one scalar field in a quoted SQL column reference.
- **JSON or JSONB** stores one schema-known document, with an explicit format
  and an optional numeric-type metadata column.
- **Composite** maps one unambiguous struct schema to a native composite
  column.
- **Timestamp** names the `timestamptz` column and, when needed, its
  nanosecond-remainder column. The caller retains the existing precision and
  synchronized-null guarantees.
- **Flattened struct** includes typed `present` and `null` predicate trees plus
  relative child bindings. Parent state is explicit; it is not inferred from
  child columns.
- **Relation** includes one table identifier and alias, a typed correlation
  predicate, optional list-state predicates, an item type and relative item
  binding, and an optional unique order tuple.

Each SQL identifier is encoded as an array of identifier components and is
quoted by the backend. A dotted string is one identifier component, not a
qualified expression. Binding and schema paths are distinct from SQL
identifier components. Binding tags are `native`, `json`, `composite`,
`timestamp`, `struct`, and `relation`. Their payload uses `path`, `column`,
`format`, `metadata`, `remainder`, `present`, `null`, `fields`, and `relation`
as applicable. Relation details use `table`, `alias`, `correlation`, `itemType`,
`item`, and `order`; order entries carry `column`, `type`, `descending`, and
`nullsFirst`.

### Typed SQL predicates and trusted registries

The built-in predicate tree uses the `trusted`, `bool`, `equal`, `null`,
`notnull`, `value`, `and`, `or`, and `not` tags. Predicate nodes use `id` for a
registered trusted predicate, `value` for a Boolean constant, `left` and
`right` for scalar operands, and `children` for Boolean composition. Scalar
values are tagged as either a `column` (with identifier components and an HFF
type) or a `literal` (with a `value` carrying an HFF kind and canonical text).
Relation and flattened-struct state can therefore be represented without
embedding arbitrary SQL.

When an application needs a developer-authored predicate outside the typed
tree, the document contains only a stable trusted-predicate ID. A host-owned
registry resolves that ID to a fixed `TrustedSQL` value and its declared
identifier references. The serialized document cannot override either the
SQL text or its references. The caller remains responsible for ensuring the
registered expression is deterministic, side-effect-free, and safe for its
configuration.

Extension method declarations are serializable data: stable ID, name,
receiver, positional/optional/variadic parameter types, result, null policy,
and base cost. SQL translator implementations are executable host code and
are represented only by stable IDs resolved in a host-owned registry. The
declaration and implementation IDs must agree. Decoding fails if a referenced
trusted predicate or translator ID is unknown; the decoder never silently
drops it.

### Validation and trust boundary

Configuration JSON is input, not trusted Go configuration. `Decode` limits the
document to 4 MiB and nested JSON arrays and objects to depth 512, counting the
root object as depth one. It rejects duplicate keys and unrecognized variants,
and validates schema, binding/schema compatibility, relation predicates, and
registry references. It does not inspect database catalogs or prove that a
compiled operation is supported by a binding; PostgreSQL `Build` performs
that operation-specific check. A decoded configuration remains data until the
host resolves registry IDs and explicitly compiles and builds a query.

The host registry is part of the trust boundary. A stable ID is not permission
to execute arbitrary code from the document; only IDs installed by the
application resolve. Build still performs backend support checks and returns
source-located errors where a program operation is unsupported.

## Alternatives considered

### Encode Go structs directly

This would be concise for exported data but fails for private immutable types,
constructor-only bindings, and function-valued options. It would also couple
the wire format to Go layout instead of preserving stable behavior.

### Serialize arbitrary SQL and callbacks

Embedding raw SQL text or callback source would make the document executable
and bypass the host's trusted code boundary. It is rejected. Stable registry
IDs keep executable behavior in application code and make missing registrations
explicit.

### Restrict version one to simple column bindings

This would simplify the first decoder but exclude the flattened and relational
storage shapes already supported by the API. A tagged binding tree and typed
predicate AST are more work, but keep version one aligned with the existing
configuration model while allowing strict validation.

## Testing

Tests round-trip supported type descriptors and binding variants, including
JSONB metadata, Timestamp remainder columns, flattened structs, relation state,
typed correlation predicates, and unique ordering. They also check strict
errors for unknown versions, fields, tags, duplicate keys, malformed paths,
mismatched schemas, invalid limits, unknown registry IDs, and unsupported
binding forms. A simple configuration test builds the same program from
original and decoded configurations and compares its `WHERE` and ordered
arguments. Decoder fuzzing under the document-size and nesting limits remains
future work.

## Limitations and open questions

Version one is implemented by the `config` package. Its public API is
`config.Encode(engine.Config, sql.Config, config.Registry)` and
`config.Decode([]byte, config.Registry) (config.Loaded, error)`, where
`config.Loaded` contains `Engine` and `SQL` configurations. The registry has
`Predicates map[string]sql.SQLPredicate` and
`Methods map[string]sql.MethodImplementation`. The format remains versioned
independently from Go struct layout. It does not serialize executable method
translators or raw trusted SQL; registry IDs are resolved by the host and
unknown IDs fail decoding.
