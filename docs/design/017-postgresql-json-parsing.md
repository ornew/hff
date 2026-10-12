# 017. PostgreSQL JSON Parsing

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-13

## Summary

The PostgreSQL backend implements `fromjson()` for string-valued and
recoverable dynamic JSON string receivers. It accepts one complete JSON
document and returns its JSON-native HFF value only after validating the
whole document, including nested object keys and numeric values. Any invalid
document produces ordinary null rather than a partial result.

## Motivation

The local evaluator rejects malformed JSON, duplicate object keys, and
numbers outside HFF's int64 or finite float64 domain. PostgreSQL's selected
SQL/JSON validation checks syntax, decoded Unicode, and key uniqueness under
the caller's no-NUL input precondition, but it does not enforce HFF's numeric
range. Returning a partially converted document would diverge from local
behavior.

## Goals

- Accept exactly one JSON value with optional JSON whitespace around it.
- Reject duplicate keys at every object nesting level and invalid JSON Unicode
  escapes.
- Preserve HFF number classification: integer-form values must fit int64;
  decimal and exponent forms must become finite float64 values.
- Treat float underflow to signed zero as valid and float overflow as invalid.
- Expose no partial value when any nested member is invalid.
- Preserve ordinary null and no-value behavior from HFF evaluation.

## Non-goals

- This design does not add PostgreSQL lowering for `bool()`, `number()`,
  `frombase64()`, `timestamp()`, or `duration()`.
- It does not make PostgreSQL enforce the local runtime's JSON-depth,
  evaluation-step, or generated-value budgets.
- It does not validate stored JSON documents during `Build` or perform database
  I/O during query construction.

## Design

### Public behavior and contracts

`fromjson()` accepts a string receiver or a recoverable dynamic JSON string.
A null receiver propagates null, a no-value receiver propagates no-value, and
a dynamic value of another runtime family produces ordinary null. The method
parses and validates the complete document before exposing the parsed value.
Invalid syntax, duplicate object keys (including nested duplicates), invalid
Unicode escapes, out-of-range integers, and non-finite or overflowing floats
produce ordinary null. Valid float underflow retains the sign when rounded to
zero. JSON whitespace may surround the single root value; trailing non-JSON
data is invalid.

PostgreSQL text cannot represent NUL. The caller therefore guarantees that
decoded JSON strings and object keys contain no NUL. This is a storage/input
precondition, not a runtime validation guarantee. Other malformed JSON text
returns ordinary null.

The PostgreSQL lowering requires PostgreSQL 16 or later because it uses
`IS JSON ... WITH UNIQUE KEYS` and `pg_input_is_valid`. PostgreSQL 18.6 is the
tested environment; the feature does not claim that other SQL backend
operations require PostgreSQL 16.

### Implementation and invariants

The translator first validates the complete text as JSON with unique keys,
then traverses the parsed JSON value and checks every number against HFF's
numeric rules. Integer spellings are accepted only when PostgreSQL can parse
them as int64. Decimal and exponent spellings use the backend's binary64
conversion path, which rejects overflow and preserves valid underflow to
signed zero. The parsed document is returned only if all nested checks pass;
otherwise the expression evaluates to SQL null.

The numeric traversal covers both arrays and object values. It does not infer
bytes, timestamps, or durations from JSON strings. Database execution limits
and caller-owned input-size bounds remain separate from HFF's local runtime
budgets.

### Compatibility and backend support

This adds PostgreSQL support for an existing HFF conversion contract; it does
not change local semantics or imply support for another backend. PostgreSQL
16+ is required for this conversion path. PostgreSQL 18.6 is the version used
for differential verification.

## Alternatives considered

### Cast the text directly to JSON

PostgreSQL JSON syntax parsing alone does not reject duplicate keys or enforce
HFF's numeric range and finite-float requirements. It therefore cannot
establish the required all-or-null result by itself.

### Return a partially converted document

This would make nested invalid numbers or duplicate keys dependent on which
fields a query later reads. The complete-document check instead gives the
same ordinary-null outcome regardless of which parsed field the predicate
uses.

## Testing

Differential PostgreSQL tests compare the result with local evaluation for
nullable SQL strings and dynamic values from native JSON and JSONB. Coverage
includes malformed syntax, nested duplicate keys, Unicode escapes, integer
overflow, float overflow and underflow, parsed field access, guards, callbacks,
and subsequent `json()` and `string()` calls. PostgreSQL 18.6 is tested; the
documented PostgreSQL 16 minimum follows from the required built-in
primitives.

## Limitations and open questions

The caller must prevent decoded NUL in JSON strings and keys. The PostgreSQL
backend does not enforce the local `MaxJSONDepth`, step, or generated-value
limits. Conversions to bytes, timestamps, durations, booleans, and numbers
remain unsupported by this backend.
