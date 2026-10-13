# 014. Lower homogeneous scalar membership directly

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Membership over native scalar values can pass through JSON encoding and
decoding even when the receiver and every non-null candidate already share
one HFF scalar type. Two paths address this shape: a direct filter predicate
for literal candidates on eligible native columns, and a native same-type
membership expression that avoids JSON conversion when values are dynamic.
The literal path can also bypass projections when composed with other
eligible scalar predicates.

The candidate lowers eligible homogeneous membership to SQL `IN`. It
preserves the string `COLLATE "C"` rule and treats null candidates as
non-matches. A nullable receiver remains null as a method result; in filter
context, the predicate remains false for a null receiver. Mixed numeric
membership and other ineligible expressions retain the general path so
cross-type exact comparison and presence behavior remain unchanged.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Directly bound native Int, String, or Bool field with same-type literal candidates at the filter root | Applied | Eligible membership composes directly with simple scalar predicates. |
| Same-type native scalar membership expression | Partial | Bool, Int, Float, String, and Bytes values can avoid JSON conversion while retaining receiver and candidate null/no-value behavior. |
| Mixed Int/Float membership | Not applied | Exact cross-subtype comparison retains the general lowering. |
| Ineligible root predicate or unsupported scalar shape | Not applied | The general lowering retains expression, type, and presence semantics. |

## Reproduction

The root-predicate comparison uses `age:in(18,21)` over a directly bound
native integer column. It distinguishes the prior JSON-based lowering, an
intermediate native-`IN` lowering that retained projection barriers, and
the direct root-predicate path. SQL text lengths and projection-barrier
counts are structural measurements, not database execution measurements.

The baseline is commit [3116dfa](https://github.com/ornew/hff/commit/3116dfa);
the implementation is commit
[88d1bb8](https://github.com/ornew/hff/commit/88d1bb8). The code is in
`backends/sql/scalar_where.go` and `backends/sql/json_membership.go`.
Correctness fixtures compare local and PostgreSQL results for nullable
Int, String, Bool, Float, and Bytes receivers, including null candidates,
computed or guarded needles, and mixed-numeric fallback.

The eligible root filter generates this predicate:

```sql
("records"."age" IS NOT NULL AND "records"."age" IN (($1::bigint),($2::bigint)))
```

With an index on `records(age)`, plain `EXPLAIN` on the temporary empty
fixture selected a Bitmap Index Scan on `records_age_idx`, represented as
`age = ANY ('{18,21}'::bigint[])`. This is a plan observation for that
fixture only.

## Results

| Lowering of `age:in(18,21)` | `WHERE` length | Projection barriers | Parameters |
|:--|--:|--:|--:|
| Prior JSON-based expression | 1,627 characters | 3 | 2 |
| Native `IN` with general root lowering | 399 characters | 3 | 2 |
| Direct literal-root predicate | 80 characters | 0 | 2 |

No execution timing or allocation comparison is reported. Plain `EXPLAIN`
was inspected only for the direct-predicate candidate; that fixture-specific
plan observation is not evidence of general index use or speed.

## Correctness and tradeoffs

The optimized paths are restricted to same-type scalar membership. Null
candidates never match a non-null receiver. Nullable receiver behavior and
argument presence remain governed by the ordinary HFF method contract.
String membership continues to use byte-lexical `COLLATE "C"`. Mixed
Int/Float equality still needs the exact general comparison path. The generic
root path retained three barriers for the measured literal filter; dynamic
expressions vary by shape. Only the eligible literal-root path removes those
barriers.

## Decision and limits

This candidate demonstrates smaller generated membership SQL for one
literal-root filter, with projection removal limited to eligible literal-root
predicates. It does not establish faster PostgreSQL execution or reduced
planning time. Retain the general lowering for ineligible or mixed-type
shapes and validate each supported form against local evaluation and
PostgreSQL.
