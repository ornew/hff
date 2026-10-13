# 018. PostgreSQL Timestamp Storage

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-13

## Summary

Store a schema-known HFF Timestamp in PostgreSQL `timestamptz`, with an optional
`smallint` column carrying the nanoseconds below the stored microsecond. The
base instant is floored to a whole microsecond; the remainder is in the range
0–999. This preserves HFF nanosecond precision without query-time rounding.

## Motivation

HFF timestamps have nanosecond precision, while PostgreSQL timestamps resolve
to one microsecond. A `timestamptz` value alone cannot retain the final three
nanosecond digits. The explicit remainder lets callers keep the native
PostgreSQL instant representation without silently changing HFF values.

## Goals

- Preserve every HFF timestamp from year 0000 through 9999 at nanosecond
  precision.
- Make sub-microsecond storage explicit and reject a timestamp `Bind` that
  could hide the precision choice.
- Preserve null consistently across the instant and remainder columns.
- Avoid rounding query values or timestamp values during SQL lowering.
- Keep timezone interpretation based on the represented instant, not the
  session timezone or the original lexical offset.

## Non-goals

- Store timestamps in JSON or JSONB. Those representations remain unsupported;
  the JSONB metadata format continues to carry numeric types only.
- Preserve the input timezone name or offset as part of a Timestamp value.
- Claim that every Timestamp method or operation is supported by PostgreSQL.
  The backend must reject unsupported operations during `Build`.

## Design

### Public binding

`BindTimestamp(path, instantColumn, options...)` binds a schema-known Timestamp
to a PostgreSQL `timestamptz` column. `WithNanosecondRemainder(column)` adds
the optional sub-microsecond column, whose physical type is `smallint`. Plain
`Bind(path, column)` rejects Timestamp values so callers choose the precision
representation explicitly. `BindTimestamp` also works as a `BindStruct` child
and as a scalar or struct `BindRelation` item. These nested bindings support
direct field predicates on flattened structs and direct field access in
`any()`/`all()` relation callbacks. A direct relation `size()` is supported via
a correlated row count, without materializing Timestamp items. Direct relation
`has(index)` supports statically known numeric or null/no-value indexes,
including nullable numeric types and negative indexes, using a count without
materializing items or requiring a unique order.
Genuinely dynamic `Any` or mixed-union indexes use the JSON fallback and may
be rejected with Timestamp items. Guarded or computed relation receivers and
operations that require complete item or list values, such as indexing or
materialization, may still fail at `Build`.
Materializing a containing struct, relation item, list, or object with a
Timestamp is not supported.

If the remainder option is absent, the caller guarantees that stored instants
are exact whole microseconds. If it is present, the caller guarantees that the
instant and remainder are either both SQL `NULL` or both non-NULL, and that the
remainder is between 0 and 999 inclusive. `Build` does not inspect rows or
validate this storage contract.

### Precision and calendar mapping

The `timestamptz` component is the instant floored to a whole microsecond. The
remainder stores the nonnegative nanoseconds between that floor and the exact
HFF instant. This definition also applies before the Unix epoch: a timestamp
one nanosecond before the epoch is stored as the preceding microsecond plus a
remainder of 999 nanoseconds, not as a negative remainder.

For SQL calendar and formatting operations, lowering decomposes epoch
nanoseconds into whole seconds with exact `div()` and `mod()` by 1,000,000,000.
If the pre-epoch remainder is negative, it decrements the quotient and adds
1,000,000,000 to the remainder. This avoids using rounded numeric `/` before
`floor()`, which can move an instant near the next-second boundary across that
boundary. This seconds decomposition is separate from the storage split above,
which floors `timestamptz` to a microsecond and stores the remaining 0–999
nanoseconds. Timestamp parity cases cover an instant immediately before a
second boundary, pre-epoch values, and the HFF year limits 0000 and 9999.

HFF's accepted years 0000–9999 map to PostgreSQL's supported date range. HFF
year 0000 maps to PostgreSQL 1 BC. The stored value represents an instant;
PostgreSQL normalizes timezone-aware input to UTC and does not retain the
original offset. The remainder supplements precision and does not change the
instant or calendar fields.

### Null and no-value

When the optional remainder is bound, both columns must be NULL together or
non-NULL together. A pair of NULL columns maps to HFF null; a non-NULL pair
maps to a Timestamp. When no remainder is bound, SQL `NULL` in the
`timestamptz` column maps to HFF null. Non-nullable schema fields still require
a non-null value. Guard-produced no-value is an evaluation state and is not a
stored timestamp representation.

### JSON and backend support

This design covers native timestamp columns only. `BindJSON` does not infer a
Timestamp from a JSON string, and timestamp JSON/JSONB storage remains
unsupported. JSONB metadata does not add timestamp tags. The backend supports
direct comparisons, `before()`, `after()`, `add(duration)`, `in()`, `exists()`,
`type()`, UTC component methods, and UTC `string()`/`json()` for native
bindings. `string:timestamp` is supported only for a literal RFC 3339 string.
Zone arguments and data-dependent timestamp parsing are unsupported. Any other
operation is rejected at build time rather than evaluated locally.

For example, a flattened profile can bind its Timestamp child and filter on
that field while keeping the parent presence and null state explicit. This
fragment assumes `e` is an engine whose schema contains nullable `profile`
with a required Timestamp `created`, and `ctx` is the build context:

```go
// The engine schema contains nullable profile with a required Timestamp child.
profile := hffsql.BindStruct(hff.Path{"profile"},
    hffsql.AsPredicate(hffsql.ColumnValue(
        hffsql.Column("records", "profile_present"), schema.BoolType,
    )),
    hffsql.AsPredicate(hffsql.ColumnValue(
        hffsql.Column("records", "profile_null"), schema.BoolType,
    )),
    hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("records", "created_at"),
        hffsql.WithNanosecondRemainder(hffsql.Column("records", "created_ns_remainder"))),
)
program, err := e.Compile(ctx,
    `profile.created:after("2026-10-01T00:00:00Z":timestamp)`,
)
query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(profile),
)
```

For relation items, direct Timestamp-field access is supported in `any()` and
`all()` callbacks, for example
`events:any(event=>event.created:after("2026-10-01T00:00:00Z":timestamp))`.
A direct `events:size` is lowered to a correlated row count. Direct
`events:has(index)` uses that count for statically known numeric or null/no-value
indexes, including nullable numeric types and negative indexes, without a
unique item order. Genuinely dynamic `Any` or mixed-union indexes use the JSON
fallback and may be rejected with Timestamp items. Guarded or computed relation
receivers and operations requiring complete relation items or lists may still
fail to build; nested field support does not permit returning the entire parent
struct or relation list as a materialized HFF value.

## Alternatives considered

### Store the timestamp in `timestamptz` alone

This is convenient and retains native PostgreSQL ordering, but loses
sub-microsecond precision. It is suitable only for data whose timestamps are
already microsecond-exact, which is why the remainder column can be omitted
only under that caller guarantee.

### Store the full instant as integer nanoseconds

An integer representation can preserve precision but does not use PostgreSQL's
native timestamp type for storage or indexing. The approved design keeps the
native instant in `timestamptz` and stores only its sub-microsecond residue
separately.

### Round to the nearest microsecond

Rounding changes HFF values and can change equality, ordering, and boundary
predicates. The design instead floors the stored base and carries the exact
remainder, so query translation does not round.

## Limitations and open questions

The physical representation and null/range invariants are fixed here. This
does not define support for JSON/JSONB Timestamp storage or broaden the set of
PostgreSQL Timestamp operations beyond those explicitly implemented and
reported by the backend profile.
