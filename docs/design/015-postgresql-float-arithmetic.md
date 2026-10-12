# 015. Exact PostgreSQL Float Arithmetic

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-13

## Summary

The PostgreSQL backend supports HFF float and mixed int/float `+`, `-`, `*`,
and `/` while preserving HFF's binary64 round-to-nearest, ties-to-even result.
It computes with exact PostgreSQL `numeric` intermediates and applies an
explicit binary64 rounding step instead of relying on PostgreSQL float
operators to produce the final result.

## Motivation

HFF specifies IEEE 754 binary64 arithmetic, including midpoint ties, subnormal
results, signed zero, and null on overflow or non-finite results. PostgreSQL
float arithmetic is not used as a proof of those language-level edge cases.
The SQL backend needs a lowering that agrees with local evaluation and
promotes an integer operand to float64 before the mixed operation, as HFF
specifies.

## Goals

- Preserve HFF's numeric result kind: integer-only `+`, `-`, `*`, and `%`
  remain int64; division and mixed int/float arithmetic return float64.
- Match binary64 round-to-nearest, ties-to-even, including exact ties,
  subnormal results, and signed zero.
- Return null for integer overflow, division by zero, float overflow, and
  non-finite results.
- Keep query operands parameterized and preserve HFF null/no-value propagation.

## Non-goals

- Float remainder; HFF remainder is int64-only.
- A performance guarantee for the exact arithmetic SQL expressions.
- Extending JSON/JSONB serialization of dynamic `Int|Float` results. Such a
  union result still requires a representation the current serializer can
  recover.

## Design

### Public behavior and contracts

Scalar `Float` arithmetic supports `+`, `-`, `*`, and `/`; mixed `Int` and
`Float` operands promote to float64. Integer-only operations retain the int64
contract, and `%` remains integer-only. Dynamic JSON arithmetic supports
`+`, `-`, `*`, `/`, `%`, and unary negation when runtime JSON numbers can be
recovered from native JSON spelling or JSONB type metadata. An incompatible
runtime value, integer overflow, division by zero, or non-finite result yields
ordinary null. HFF's operand presence and no-value rules remain in force.

### Implementation and invariants

Each finite binary64 operand is reconstructed as an exact decimal rational from
its IEEE sign, significand, and exponent. Addition, subtraction, and
multiplication then produce an exact `numeric` value. The result is rounded to
binary64 and checked against explicit boundaries: overflow begins at the
rounding midpoint `2^1024 - 2^970`; values at or below half the minimum
subnormal, `2^-1075`, round to signed zero as required.

Division requires a rational result that may not have a finite decimal
expansion. The lowering computes a quotient candidate at 1000 decimal places,
then uses exact cross-products to detect whether the true quotient is exactly
on a binary64 midpoint. If a candidate lies on such a midpoint and its
significand is odd, it is adjusted to the even adjacent value. For a ratio of
finite binary64 operands, the reduced denominator is at most `2^2098`;
binary64 midpoints have denominators at most `2^1075`. A non-tie quotient is
therefore separated from a midpoint by at least `2^-3173`. Rounding the
candidate to 1000 decimal places introduces error no greater than `5e-1001`,
so it cannot cross a non-tie midpoint. Exact cross-products handle equality rather than
inferring a tie from the approximation.

JSON arithmetic first resolves integer and float candidates using the schema,
native JSON number spelling, and, for JSONB, the configured numeric metadata.
Integer-only dynamic operands use the checked int64 lowering; other supported
numeric combinations use the exact float path. JSONB metadata is needed when
storage has erased an integer/float or value distinction that the operation
observes. This does not make every dynamic result serializable: dynamic
`Int|Float` results passed to `json()` or `string()` remain unsupported.

### Compatibility and backend support

The change adds PostgreSQL support without changing the language contract in
`spec/types.md` or `spec/evaluation.md`. Other SQL dialects do not inherit this
implementation. Float remainder, non-finite stored inputs, and JSONB
distinctions not recoverable from schema or metadata remain unsupported or
outside the caller's storage contract. See the
[PostgreSQL numeric guide](../backends/postgresql/json.md#numeric-arithmetic)
for user-facing examples and metadata requirements.

## Alternatives considered

### Use PostgreSQL `double precision` operators directly

This is concise, but it does not establish HFF's exact midpoint and boundary
behavior across all binary64 values. It is not used for the final rounding
step.

### Use a fixed decimal approximation without a tie check

A fixed precision separates non-ties when its error is below the minimum
distance to a rounding midpoint, but an exact midpoint still needs explicit
ties-to-even handling. The selected division path combines a high-precision
candidate with exact cross-products and even-neighbor correction.

## Testing

The SQL arithmetic tests compare completed PostgreSQL results with local HFF
results at ordinary values, exact midpoint cases, adjacent values, overflow,
the half-minimum-subnormal boundary, subnormals, and signed zero. Dynamic JSON
tests also cover numeric-kind guards, values, and null/no-value propagation for
native JSON and metadata-restored JSONB. JSON decoding cases include decimal
spellings below the float64 range, which must become signed zero instead of a
database cast error. The current validation set includes 300 typed-float
comparisons and 1,440 dynamic JSON/restored-JSONB operator, type, value, and
guard comparisons; these are regression evidence, not an exhaustive
enumeration of binary64 inputs.

## Limitations and open questions

The SQL expression is a correctness-first lowering, not a latency or SQL-size
guarantee. Dynamic `Int|Float` results cannot yet be serialized by `json()` or
`string()`, and JSONB cannot recover distinctions that neither its stored
value nor configured metadata records. These are separate representation
limitations from the arithmetic result itself.
