# 016. Canonical PostgreSQL JSON Serialization

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-13

> Float-specific decimal spelling and signed-zero output details are superseded
> by [021. Backend-Native Approximate Float Semantics](021-backend-native-float-semantics.md).
> This record's JSON structure, key ordering, escaping, null, and no-value
> contracts remain in force.

## Summary

PostgreSQL `json()` and `string()` serialize schema-known values and recovered
JSON-native `Any`/union values using HFF's canonical representation. The
lowering preserves numeric kinds and recursively handles supported arrays and
objects instead of relying on PostgreSQL's JSONB text formatting.

## Motivation

PostgreSQL JSON and JSONB have different storage behavior from HFF values.
JSONB can normalize integer and float spellings, discard negative zero, and
choose an object-key order unrelated to HFF's canonical output. A direct cast
to text therefore cannot implement `json()` faithfully for dynamic values or
collections. The serializer must use the schema and configured numeric
metadata to recover distinctions required by the operation.

## Goals

- Emit compact JSON with array order preserved and object keys sorted by
  PostgreSQL `C` collation.
- Preserve JSON-native scalar kinds, including integer versus float, when they
  are recoverable from schema, native JSON spelling, or JSONB metadata.
- Serialize nested JSON-native lists, maps, structs, and supported dynamic
  unions without changing their values.
- Keep `string()`'s unquoted root-string behavior distinct from `json()`'s
  quoted string encoding.
- Preserve null and no-value behavior and reject plans whose required numeric
  representation cannot be recovered.

## Design

### Public behavior and contracts

`json()` emits compact canonical JSON. `string()` leaves a root string
unquoted and otherwise formats scalar values or collections according to the
HFF method contract. A null receiver returns the string `"null"`; a no-value
receiver propagates no-value. JSON null inside a collection remains a JSON null
element or member.

Integers serialize as integer decimal text. Floats use a shortest
round-tripping decimal with an explicit float marker, canonical exponent form,
and preserved signed zero. Strings use JSON escaping in `json()` and as nested
values in collection output. Arrays retain input order; object keys are sorted
by `C` collation. JSONB metadata text restores the numeric kind and value when
JSONB storage discarded a distinction that serialization observes; it does
not promise to preserve the user's original exponent spelling.

The supported dynamic domain is JSON-native: null, booleans, strings,
recoverable numbers, arrays, and objects. Schema-known operations and
JSON-native `Any`/union results can be serialized when the declared schema and
storage representation determine their numeric forms. This includes supported
numeric arithmetic results and nested collections. Arbitrary HFF bytes,
timestamps, and durations inside dynamic JSON are not inferred from JSON
strings. `fromjson()` and conversions that require reconstructing those
non-JSON HFF types are separate from serialization. The HFF conversion
contracts are defined independently; PostgreSQL parsing behavior for
`fromjson()` is recorded in [017](017-postgresql-json-parsing.md).

### Implementation and invariants

For schema-known values, the serializer selects each scalar and collection
branch from the declared HFF type and normalizes children before encoding.
For a recovered dynamic value, lowering first constructs a normalized
JSON-native document using schema candidates and metadata where available. A
recursive token traversal then emits scalar encodings, array delimiters and
ordered children, or object members sorted with `C` collation. This avoids
using `jsonb::text` as a canonical representation and keeps JSONB's physical
key order from affecting output.

Float provenance is tracked through fields, indexes, callback parameters,
transformations, and folded accumulator state. Serialization rejects a plan
when JSONB has erased a float distinction such as negative zero and no metadata
restores it. It does not reject a result merely because an unused source value
had a lossy representation. Integer JSON values are normalized through the
schema-aware int64 path; float values use the canonical binary64 formatter.

SQL lowering does not promise enforcement of the local runtime's
`MaxJSONDepth`, `MaxSteps`, or generated-value budget. `Build` does not execute
the expression; database execution limits and data-size bounds remain the
caller's responsibility. No PostgreSQL callback evaluation order is implied.

### Compatibility and backend support

This is PostgreSQL-specific lowering of existing HFF method behavior; it does
not change the language contract or imply support in another SQL backend.
Unrecoverable JSONB numeric representations and dynamic non-JSON HFF types
remain unsupported. `fromjson()` parsing is implemented separately and does
not imply support for conversions to bytes, timestamps, or durations.

## Alternatives considered

### Cast the stored JSON or JSONB value to text

This is simple, but JSONB may have erased integer/float distinctions and
negative-zero sign, and its object order is not HFF's canonical order. A cast
cannot restore information already lost by storage.

### Serialize only from the declared type

This works for schema-known fields but cannot handle recovered dynamic
`Any`/union results or values produced by supported arithmetic and callbacks.
The selected lowering combines schema-guided normalization with recursive
serialization while rejecting unrecoverable representations.

## Testing

Differential PostgreSQL tests compare completed `json()` and `string()` results
with local HFF results for native JSON and metadata-restored JSONB. The tested
profile includes `Any`, numeric and string unions, nulls, nested list/map
values, constructors, callback results, arithmetic results, whitespace in
source JSON, Unicode, control/HTML characters, signed zero, subnormals, and
nested collections. These cases establish the listed profile; they do not
prove behavior for every possible PostgreSQL document or depth.

## Limitations and open questions

Dynamic JSON serialization is limited to JSON-native values whose required
numeric representation can be recovered. Metadata restores numeric kinds and
values only. Bytes, timestamps, and durations require separate backend
implementations under their existing HFF conversion contracts. The
SQL serializer has no local runtime JSON-depth or step-budget guarantee, and
no latency or SQL-size guarantee is made.
