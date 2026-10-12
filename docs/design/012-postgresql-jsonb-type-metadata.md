# 012. PostgreSQL JSONB Type Metadata

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-12

## Summary

The accepted schema-aware PostgreSQL JSONB design uses ordinary JSONB storage
plus a separate JSON-Pointer-indexed type-metadata value when stored JSONB
cannot faithfully preserve the HFF representation. Translation must reject an
ambiguous value when its HFF type cannot be recovered from the declared schema
and supplied metadata.

## Motivation

PostgreSQL JSONB normalizes JSON representation. In particular, a numeric
value's original spelling may not reveal whether the HFF value was an integer
or float, and some float details such as negative zero are representation
significant. Treating every decoded JSON number as one type would silently
change HFF behavior in dynamic values and transformations.

## Goals

- Keep ordinary PostgreSQL JSONB as the data representation.
- Carry HFF type information separately at JSON Pointer paths where JSONB
  cannot preserve the needed distinction.
- Preserve the distinction between null, missing keys, integers, and floats.
- Reject unsupported ambiguous values instead of guessing from PostgreSQL's
  normalized textual rendering.
- Keep JSON and JSONB bindings explicit and schema-aware.

## Non-goals

- Replacing JSONB with a tagged or custom PostgreSQL storage type.
- Inferring HFF type from SQL column declarations or reading database rows at
  Build time.
- Making every dynamic or representation-sensitive HFF value translatable
  before metadata support is implemented and verified.

## Design

### Public behavior and contracts

A JSONB binding names the ordinary JSONB data column and supplies a separate
metadata value indexed by JSON Pointer. The metadata identifies the HFF type
only where needed to reconstruct the represented value. Schema information
continues to determine statically known portions of the value; metadata does
not replace the HFF schema or turn an unsupported operation into a supported
one.

If both schema and metadata fail to identify a value's HFF type faithfully,
Build rejects the operation with a source-located backend error. The translator
must not infer integer versus float, normalize negative zero, or conflate
missing and null based solely on PostgreSQL JSONB output.

### Implementation and invariants

Metadata is separate from JSONB data, so the JSONB document remains usable by
ordinary PostgreSQL JSON facilities. Metadata lookup is by JSON Pointer and
must remain associated with the same stored document and binding. Nested paths
must use a stable pointer encoding. Data and metadata are treated as caller-
owned storage; HFF does not execute queries or inspect rows during Build.

### Compatibility and backend support

Native JSON can preserve numeric spelling needed by supported profiles.
JSONB translation requires schema knowledge and, for otherwise ambiguous
values, the separate metadata. Operations whose semantics cannot be restored
from this information remain Build errors. Local evaluation is independent of
PostgreSQL storage metadata.

## Alternatives considered

### Tagged JSONB in one column

Embedding HFF tags in every JSON value could preserve types, but changes the
document consumed by ordinary PostgreSQL JSON operators and application code.
The accepted design keeps JSONB ordinary and places type metadata separately.

### Restrict JSONB to only unambiguous values

This is safe without metadata, but prevents supported queries over stored
dynamic or representation-sensitive values when their type can be provided
explicitly. Metadata is accepted as the extension point; any remaining
ambiguity is still rejected.

## Testing

Tests should verify JSON and JSONB representation parity when schema and
metadata suffice, including integer/float distinctions, negative zero, nested
collections, missing versus null, and rejection when metadata is absent or
conflicting. They must use real PostgreSQL storage and read-back for cases
where JSONB normalization matters.

## Limitations and open questions

The storage choice is accepted. Exact metadata structs, SQL binding options,
and the operations enabled by each metadata kind are implementation details
that must be documented with the delivered API. Metadata does not promise
support for every `Any` value or operation.
