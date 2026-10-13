# 020. PostgreSQL JSON Timestamp Values

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-13

## Summary

Schema-known HFF Timestamps stored in PostgreSQL JSON or JSONB use the canonical
UTC RFC 3339 string representation, with up to nine fractional-second digits.
The translator interprets a JSON string as a Timestamp only when the bound
schema declares that type. It preserves nanoseconds and HFF's year range,
including year 0000, and rejects plans whose active type or requested operation
cannot be recovered faithfully.

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

The translator represents an instant as exact PostgreSQL `numeric` containing
total Unix epoch nanoseconds. This value is not restricted to `int64`; the
complete HFF year range exceeds that integer range. Parse the canonical UTC
fields and construct the total with exact arithmetic, without passing through
`timestamptz`'s microsecond precision or a rounded quotient. Comparisons,
membership, and duration addition use the exact value. PostgreSQL calendar
operations that encounter HFF year 0000 map it to 1 BC and back without
changing the HFF year.

### PostgreSQL operation subset

For schema-known JSON/JSONB Timestamp values, the supported subset is direct
comparison, `before()`, `after()`, `add(duration)`, `in()`, `exists()`,
`type()`, UTC component extraction, and canonical UTC serialization through
zero-argument `any:string()` and `any:json()`. The separate
`timestamp:string(tz)` overload takes a time-zone argument and is outside this
subset. `sort()` is also supported for schema-known lists of Timestamps and
orders them by exact instant. Other collection transformations are supported
only where the binding and operation can be lowered without materializing an
unsupported representation.

Zone arguments, data-dependent string parsing, ambiguous unions, and
operations whose SQL lowering cannot preserve the active Timestamp
representation are Build errors. Build performs no row validation and does
not silently fall back to local evaluation.

### Null and presence

JSON null maps to HFF null when permitted by the declared schema. A missing
object member and a guard-produced no-value retain their existing HFF presence
semantics; neither is a Timestamp encoding. Nullable schema fields may accept
null, while a non-nullable field remains subject to normal input validation.

## Compatibility and backend support

This design changes no HFF syntax or local evaluation semantics. JSON and
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
migration contract. This design keeps metadata version 1 numeric-only and
rejects genuinely ambiguous unions.

## Testing

Required conformance tests compare PostgreSQL results with local evaluation for
schema-known Timestamp values in JSON and JSONB. They cover nullable and nested
fields, typed collection elements where supported, the operation subset above,
and canonical serialization. Boundary cases include nanoseconds on either
side of a second boundary, pre-epoch instants, years 0000 and 9999, and
different zone offsets normalized to the same instant. Tests confirm that a
JSON string under `Any` remains an HFF String, that Build rejects ambiguous
`String|Timestamp` and `Bytes|Timestamp` operations, and that null, missing
members, and no-value remain distinct. Storage fixtures use canonical values
consistent with their declared schema; Build does not validate stored rows.

## Limitations and open questions

The supported operation list is an initial subset, not a promise that
all JSON/JSONB collection or callback transformations can be lowered. Any
extension of the metadata format or support for ambiguous string-backed unions
requires a separate design.
