# 020. PostgreSQL JSON Timestamp Values

- **Status**: Proposed
- **Author**: @ornew
- **Date**: 2026-10-13

## Summary

Schema-known HFF Timestamps stored in PostgreSQL JSON or JSONB should use the
canonical UTC RFC 3339 string representation, with up to nine fractional-second
digits. The translator should interpret a JSON string as a Timestamp only when
the bound schema declares that type. It must preserve nanoseconds and HFF's
year range, including year 0000, and reject plans whose active type or requested
operation cannot be recovered faithfully.

## Motivation

JSON and JSONB preserve string values, making a canonical Timestamp string a
portable storage representation. Casting that value directly to PostgreSQL
`timestamptz` is insufficient: PostgreSQL stores microseconds, while HFF
timestamps have nanosecond precision, and PostgreSQL's calendar does not use
HFF's year numbering. JSONB's existing type metadata is numeric-only and should
not be repurposed for Timestamp tags.

## Goals

- Decode schema-known Timestamp values from JSON and JSONB strings, including
  nullable fields and nested schema-known values where their access shape is
  supported.
- Preserve nanoseconds, timezone-normalized instant semantics, and years 0000
  through 9999.
- Keep schema interpretation explicit: a JSON string under `Any` remains a
  String.
- Reject ambiguous type recovery and unsupported SQL lowering during `Build`.
- Keep JSONB metadata version 1 limited to numeric representation recovery.
- Preserve null, missing-member, and no-value behavior independently.

## Non-goals

- Adding Timestamp tags to JSONB metadata or changing application JSON into a
  tagged/custom representation.
- Inferring Timestamp values from strings under `Any`.
- Treating JSON/JSONB storage as equivalent to native `timestamptz` bindings.
- Supporting every Timestamp method or every dynamic union operation in the
  initial backend subset.
- Validating database rows or metadata by performing I/O during `Build`.

## Design

### Storage and type recovery

Store a Timestamp as its canonical HFF UTC RFC 3339 string. The formatter uses
four-digit years, omits trailing fractional zeros, and emits at most nine
fractional digits. For example, a value with nanosecond fraction 123 is stored
as `"2026-10-01T00:00:00.000000123Z"`; year zero remains a four-digit
`0000` year. The string represents an instant, not the source offset.

The schema determines whether a string is a Timestamp. A schema-known nullable
Timestamp may map JSON null to HFF null. Nested Timestamp fields, list elements,
and map values may be decoded where the declared schema and JSON path allow
typed access. A string under `Any` remains a String. Unions whose alternatives
share the JSON string category, including `String|Timestamp` and
`Bytes|Timestamp`, are ambiguous without a separately designed metadata tag;
`Build` must reject operations that require the unknown alternative. JSONB
metadata version 1 continues to describe numeric types only.

The application stores values that conform to the declared schema and
canonical Timestamp representation. `Build` performs no database I/O and
validates neither rows nor complete JSON documents. Invalid or noncanonical
stored Timestamp text violates the caller's storage contract; the design does
not assign a special interpretation to such rows.

### Exact PostgreSQL translation

The translator must not reduce a Timestamp to one integer containing total
epoch nanoseconds: the full year range does not fit in `int64`. It should keep
the instant as a pair of whole epoch seconds and a nanosecond fraction, with a
normalized fraction in the range 0–999,999,999. Convert the canonical UTC date
and time fields to whole seconds, then compare and add durations using exact
integer arithmetic on the pair. Carry or borrow across a second boundary
without rounding the fractional part.

For pre-epoch instants, normalize a negative remainder by decrementing the
whole-second quotient and adding one billion to the remainder. Map HFF year
0000 to PostgreSQL 1 BC when using PostgreSQL calendar operations. This keeps
the representation ordered and preserves the complete HFF year range without
passing nanoseconds through `timestamptz`'s microsecond precision or through a
rounded numeric division.

### Initial PostgreSQL operation subset

For schema-known JSON/JSONB Timestamp values, the initial supported subset
should include direct comparisons, `before()`, `after()`, `add(duration)`,
`in()`, `exists()`, `type()`, UTC component extraction, and canonical UTC
serialization through zero-argument `any:string()` and `any:json()`. The
separate `timestamp:string(tz)` overload requires an explicit time-zone
argument. Typed nested field and collection access is supported only where the
corresponding JSON binding and operation can be lowered without materializing
an unsupported representation. `string:timestamp` may convert a literal RFC
3339 string; data-dependent string parsing is not included in this subset.

Zone arguments, unsupported methods, ambiguous unions, and operations whose
SQL lowering cannot preserve the active Timestamp representation are Build
errors. The translator must not silently fall back to local evaluation or
pretend that a JSON value has been validated at Build time.

### Null and presence

JSON null maps to HFF null when permitted by the declared schema. A missing
object member and a guard-produced no-value retain their existing HFF presence
semantics; neither is a Timestamp encoding. Nullable schema fields may accept
null, while a non-nullable field remains subject to normal input validation.

## Compatibility and backend support

This proposal changes no HFF syntax or local evaluation semantics. JSON and
JSONB remain ordinary application-readable documents. Existing numeric JSONB
metadata remains unchanged and cannot identify string-backed types. Native
PostgreSQL `BindTimestamp` storage remains a separate representation with its
own `timestamptz` and optional nanosecond-remainder contract.

## Alternatives considered

### Cast JSON strings to `timestamptz`

This reuses PostgreSQL's timestamp parser and comparisons, but loses sub-
microsecond precision and can round during conversion. PostgreSQL can represent
HFF year 0000 by mapping it to 1 BC, but a direct cast does not apply that HFF
mapping. A direct cast therefore cannot serve as the semantic representation.

### Store total epoch nanoseconds as `bigint`

This is exact only for a much smaller date range than HFF supports. Keeping
whole seconds and a nanosecond fraction avoids that overflow while preserving
exact ordering.

### Add Timestamp metadata tags to JSONB

Type tags could disambiguate Timestamp from other string-backed types, but
would expand the caller-owned metadata format and require a versioning and
migration contract. This proposal keeps metadata version 1 numeric-only and
rejects genuinely ambiguous unions.

## Testing

An implementation should compare PostgreSQL results with local evaluation for
schema-known Timestamp values in JSON and JSONB. Include nullable and nested
fields, typed collection elements, direct comparisons, arithmetic, component
extraction, and serialization. Boundary cases should include nanoseconds just
before and after a second boundary, pre-epoch instants, year 0000, year 9999,
and timezone offsets normalized to the same instant.

Tests should confirm that a JSON string under `Any` remains an HFF String; a
Timestamp type guard on that value should fail with no-value rather than infer
a Timestamp. Build tests should reject ambiguous `String|Timestamp` and
`Bytes|Timestamp` operations, and operations outside the supported subset.
Storage fixtures should use canonical values that satisfy the declared schema.
Verify that null, missing members, and no-value remain distinct. These are
proposed validation cases, not evidence that the backend implementation exists.

## Limitations and open questions

The exact SQL expression shape and performance remain to be implemented and
measured. The supported operation list is an initial subset, not a promise that
all JSON/JSONB collection or callback transformations can be lowered. Any
extension of the metadata format or support for ambiguous string-backed unions
requires a separate design.
