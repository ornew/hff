# 021. Backend-Native Approximate Float Semantics

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-14

## Summary

HFF Float values remain approximate binary floating-point values, but their
parsing, arithmetic, rounding, aggregation, and text formatting follow the
selected backend's declared native model. A backend exposes these details in
its compatibility `Profile`. Integer exactness, numeric subtype identity, and
exact mixed Int/Float comparisons remain language guarantees.

## Motivation

Requiring every backend to reproduce one Float parser, arithmetic pipeline,
rounding rule, and shortest decimal formatter creates large translation costs
and SQL expressions without making Float exact. Backend-native behavior is a
clearer contract for this approximate type, provided applications can inspect
the model and Int semantics remain exact.

## Goals

- Define Float as approximate and make backend-specific results inspectable.
- Preserve Int as exact int64 and preserve Int versus Float subtype markers.
- Keep mixed Int/Float equality and ordering exact over the represented values.
- Return null for division by zero, Int overflow, invalid Float conversions,
  non-finite results, and other operation failures required to fail.
- Make Float aggregation follow the selected backend's Float model when those
  methods are implemented.

## Non-goals

- A cross-backend guarantee for identical Float result bits or decimal text.
- A strict compatibility mode that restores one global Float implementation.
- Changing Int arithmetic, Int range checks, or exact mixed numeric comparison.
- Claiming Float aggregation methods are implemented by this decision.

## Design

### Public behavior and contracts

Float is an approximate numeric subtype. Decimal and exponent numeric spellings
select Float; integer spellings select Int. Query literals are parsed by the
core compiler into Float values before backend translation. A backend may use
its native Float parser for runtime conversions and stored values, arithmetic,
rounding operations, and formatter. Consequently, Float
results near rounding boundaries, underflow handling, aggregate precision,
and serialized decimal spellings can differ between backends. The backend's
`Profile` declares its parsing, arithmetic, rounding, formatting, and—when
available—aggregation model.

Float values must remain distinguishable from Int values in observable
results. JSON output therefore retains a decimal point or exponent marker for
Float values, even when the backend's exact decimal spelling differs. A
backend's JSONB metadata contract may preserve numeric subtype and additional
Float text where storage would otherwise erase an observable distinction.
That metadata restores representation; it does not make Float arithmetic
exact or impose another backend's Float model.

Int remains exact signed 64-bit arithmetic. Mixed Int/Float arithmetic uses
the language's numeric promotion rule and follows the selected backend's
approximate Float model. Mixed equality and ordering compare exact represented
values and do not round an Int through Float first. Division by zero, Int
overflow, invalid conversion, non-finite Float results, and other required
operation failures yield null.

Selection operations such as min/max preserve the selected input value and
subtype; they do not reformat or recompute it. Their mixed numeric comparison
uses exact represented values. Float `sum()` and `avg()` instead combine Float
values and therefore follow the backend model described above when implemented.

Float `round()`, `ceil()`, and `floor()` follow the backend's declared native
rounding behavior. Int `round()`, `ceil()`, and `floor()` are identity
operations. Float `sum()` and `avg()` are planned to follow the selected
backend's declared Float model; these aggregate methods are not implied to be
available until separately implemented and exposed by the backend.

### Implementation and invariants

Each backend reports stable descriptive `float.*` details through `Profile`.
At minimum, the details identify Float model, parsing, arithmetic, rounding,
formatting, and any execution requirements. Implemented Float aggregates add
an aggregate detail. Profiles describe behavior rather than enabling methods
or changing service policy.

The Local backend uses Go `float64` parsing and arithmetic. PostgreSQL uses
its native `double precision` model for supported ordinary Float operations;
where its implementation needs a guarded fallback for extreme magnitudes, the
profile describes that fallback and its rounding/zero-sign limits. PostgreSQL
input validation requires version 16 or later. The PostgreSQL text format used
to transport Float values through JSON requires `extra_float_digits > 0`;
PostgreSQL's default satisfies this requirement. HFF neither sets nor inspects
these settings, so the caller must ensure they are configured. Conversion
paths validate inputs and outputs before returning an HFF Float. The
PostgreSQL `round(double precision)` midpoint behavior is ties-to-even on the
supported platform; the Local backend's `math.Round` rounds midpoint ties
away from zero. Stored schema-bound data remains a caller storage contract:
values must conform to
the declared type and be finite. A conversion from arbitrary text or JSON is
a separate checked operation and returns null when the backend rejects the
input or result.

### Compatibility and backend support

This decision replaces the exact binary64 Float arithmetic decision in
[015](015-postgresql-float-arithmetic.md) and supersedes its Float-specific
rounding, parsing, and output assumptions. It also supersedes the Float
spelling and signed-zero formatting requirements in
[016](016-postgresql-json-serialization.md); that record's JSON structure,
key ordering, escaping, null, and no-value rules remain in force.

Applications that require stable cross-backend exact arithmetic should use
Int where its range is sufficient. Applications using Float should inspect
the built query/filter `Profile` and account for the declared backend model.
No strict compatibility mode is provided.

## Alternatives considered

### Preserve one exact binary64 contract in every backend

This makes results more uniform but requires each backend to emulate parsing,
ties, subnormals, overflow, signed zero, and decimal formatting. PostgreSQL
then needs substantial exact-numeric SQL for behavior already represented by
its native Float type. The design rejects that cost for an approximate type.

### Offer a compatibility switch

A switch would create two Float contracts for every backend and complicate
profiles, tests, and query plans. No concrete caller requirement justified
maintaining both modes, so the model is backend-native without a strict mode.

## Testing

Backend tests must validate each profile's declared parsing, arithmetic,
rounding, serialization, and failure behavior at ordinary values and relevant
boundaries. Differential tests remain useful for Int behavior, exact mixed
numeric comparisons, null/no-value behavior, and subtype preservation. They
must not require Float bitwise or textual equality across different backend
models. Float aggregate tests are required when those methods are implemented.

## Limitations and open questions

Float aggregate methods and profile details for them remain future work. The
PostgreSQL implementation's exact supported fallback boundaries and any
database-setting dependencies are documented in its profile and backend guide.
Backend-native Float behavior is not a promise of identical results across
PostgreSQL versions or other SQL engines.
