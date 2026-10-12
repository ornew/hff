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
  merely because a metadata column is present.

## Design

### Public behavior and contracts

A JSONB binding names the ordinary JSONB data column and may supply a separate
version-1 metadata column indexed by JSON Pointer. The public option is
`WithTypeMetadata(column)`, used with
`BindJSON(path, dataColumn, JSONB, ...)`. Metadata identifies numeric HFF types
where JSONB cannot preserve them; it supplements, rather than replaces, the
HFF schema.

If the declared schema, configured metadata binding, and operation cannot
support faithful lowering, `Build` rejects the plan with a source-located
backend error. The translator must not infer integer versus float, normalize
negative zero, or conflate missing and null based solely on PostgreSQL JSONB
output. Build does not inspect stored metadata contents.

### Implementation and invariants

Metadata is separate from JSONB data, so the JSONB document remains usable by
ordinary PostgreSQL JSON facilities. Version 1 uses an object with a `version`
number and a `types` object keyed by JSON Pointer. For example,
`{"version":1,"types":{"/price":{"type":"float","text":"-0.0"}}}`
records a float at `/price`. Pointer segments escape `~` as `~0` and `/` as
`~1`; array indexes are zero-based decimal segments, and the empty key is `/`.
The `type` marker records `int` or `float`. Optional `text` is used when the
JSONB value alone cannot preserve the exact HFF number, including negative
zero.

The caller stores the data and metadata together and guarantees that the
version, paths, numeric markers, optional text, and declared schema agree.
`Build` performs no database I/O, row inspection, or full-document metadata
validation. It rejects unsupported or unrecoverable plans. Metadata currently
restores numeric representations only; bytes, timestamps, and durations are
not supported metadata types. JSON-native kinds continue to use the data
document and schema.

### Compatibility and backend support

Native JSON can preserve numeric spelling needed by supported profiles.
JSONB translation uses schema knowledge and, for numeric distinctions lost by
JSONB, the separate metadata. The current implementation restores numeric
subtypes and text only; operations requiring unsupported metadata kinds or
other unavailable representation remain Build errors. Local evaluation is
independent of PostgreSQL storage metadata.

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
collections, and missing versus null. Build-time tests cover invalid binding
configuration and plans whose HFF representation is unrecoverable. Stored
metadata consistency is a caller contract, not a runtime validation performed
by HFF. Tests use real PostgreSQL storage and read-back where JSONB
normalization matters.

## Limitations

The version-1 JSON Pointer format and `WithTypeMetadata` binding option are
implemented for numeric type restoration. This is not general dynamic-value
serialization: metadata does not make every `Any` value, union, or operation
supported. If the schema, JSON value, and numeric sidecar cannot recover the
required HFF representation, `Build` rejects the plan.
