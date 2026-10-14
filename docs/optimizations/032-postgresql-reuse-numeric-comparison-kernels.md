# 032. Reuse numeric comparison kernels within a scope

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

Repeated mixed Int/Float comparisons could rebuild the same Float floor and
the same numeric ordering projection. A JSON-backed scalar pair could also
repeat decoding the same stored text as the same numeric family. Within a
projection scope, the translator now reuses these kernels:

- Native comparisons share a Float floor projection by Float expression and
  an ordering projection by the ordered Int/Float expression pair. The raw
  numeric values are shared; each comparison still applies its own operator,
  operand direction, and presence handling.
- A scalar JSON pair shares decoding only for identical text and numeric
  family within its isolated pair subquery. The cache key retains ambiguity
  checks and any predecoded Float override, so different representation
  requirements do not alias.

JSON pairs with multiple numeric-family branches or mixed Int/Float
comparisons already project their guarded casts. The decode cache reuses
those projections; single-family pairs retain their existing inline casts.

Callback, fold, relation, equality, and filter scope boundaries remain in
force. The JSON pair cache is not hoisted out of its subquery. Stored-value
representability, schema, and JSONB metadata checks still run before numeric
comparison. This changes generated SQL shape, not comparison semantics.

## Applicability

| Workload | Status | Reason |
|:--|:--|:--|
| Repeated native mixed Int/Float comparisons in one projection | Applied | Reuse the Float floor and ordered numeric result while retaining per-comparison operators and presence. |
| Identical Float operand compared with multiple Int operands | Applied | Share the Float floor; keep separate order projections for distinct typed operand pairs. |
| Identical text decoded as the same numeric family within one scalar JSON pair | Applied | Reuse the text/family decode while retaining ambiguity and Float-override identity. |
| Distinct JSON operands or independently required decode variants | Not applied | Keep separate projections when text, family, ambiguity, or Float override differs. |
| Independent callback, fold, relation, JSON-pair, and filter scopes | Not applied | Preserve existing cache resets and evaluation scope. |
| Single native comparison, distinct JSON pair, and unconstrained `Any` pair controls | Not applied | No repeated eligible kernel exists, or the generic path remains required. |

## Reproduction

The shape baseline is commit
[0a435fd](https://github.com/ornew/hff/commit/0a435fd76eb5d70df382b86bc4ad37a7cc5dadf5).
The candidate implementation accompanies this record. The temporary shape
fixture used Go 1.27.1 and PostgreSQL 18.6 on aarch64 with
`extra_float_digits=1`; correctness parity also ran with `extra_float_digits`
set to 3. Native bindings included nullable Int fields `i` and `j`, and a
nullable Float field `f`. The SQL-shape fixture bound nullable
`String|Int|Float|Null` fields `choice` and `other`, an `Any` field `data`, a
list of the same union as `items`, and a nullable-Float list `floats` to native
PostgreSQL `json` columns. The parity fixture covered both JSON and JSONB,
with numeric metadata where its schema could not recover subtype or
representation.

Representative filters were:

```hff
i<f i!=f f>i
i<f j<f
(i<f | j<f) !(i=f)
choice=choice
items:any(x=>x=x)
floats:any(x=>i<x j<x)
floats:any(f=>i<f j<f)
floats:any(x=>floats:any(x=>i<x j<x))
```

Durable fixtures are the [unit tests](https://github.com/ornew/hff/blob/main/backends/sql/numeric_kernel_reuse_test.go),
[PostgreSQL parity tests](https://github.com/ornew/hff/blob/main/backends/sql/numeric_kernel_reuse_integration_test.go),
and [cache-identity controls](https://github.com/ornew/hff/blob/main/backends/sql/numeric_kernel_cache_integration_test.go).
The shape generator and raw SQL/plans were temporary files outside the
repository. For the plain-plan fixture, the temporary table had columns
`i bigint`, `j bigint`, `f double precision`, and `choice`, `other`, `data`,
`items`, and `floats` as `json`; each generated filter was compiled and
explained with `EXPLAIN (COSTS OFF)` against the empty table. The durable tests
cover 171 rows across 24 queries, JSON/JSONB formats, and Float settings 1 and
3, including captures, shadowing, nested callbacks, guards, null and empty
values, signed zero, subnormals, the int64 limits, and values around 2^53.

The focused checks were:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^(TestNumericKernelReuse.*|TestMixedNumericOrderKeepsPresenceOutsideCache|TestJSONNumericFamiliesKeepGuardAndOverrideIdentity)$' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^(TestPostgreSQLNumericKernelReuseMatchesLocal|TestPostgreSQLJSONNumericFamilyCacheIdentity)$' -count=1
```

## Results

Generated `WHERE` bytes and `OFFSET 0` barriers were counted for the same
filters on baseline and candidate. The validation column is the number of
`pg_input_is_valid()` calls in the generated expression.

| Filter | Baseline bytes / barriers / validations | Candidate bytes / barriers / validations |
|:--|--:|--:|
| `i<f i!=f f>i` | 2,443 / 12 / 0 | 1,205 / 8 / 0 |
| `i<f j<f` | 1,763 / 10 / 0 | 1,534 / 9 / 0 |
| `(i<f \| j<f) !(i=f)` | 2,688 / 15 / 0 | 1,838 / 12 / 0 |
| `choice=choice` | 3,431 / 14 / 2 | 2,339 / 9 / 1 |
| `items:any(x=>x=x)` | 3,956 / 18 / 2 | 2,853 / 13 / 1 |
| `floats:any(x=>i<x j<x)` | 2,641 / 15 / 1 | 2,411 / 14 / 1 |
| `floats:any(f=>i<f j<f)` | 2,641 / 15 / 1 | 2,411 / 14 / 1 |
| `floats:any(x=>floats:any(x=>i<x j<x))` | 3,159 / 19 / 1 | 2,931 / 18 / 1 |
| Single comparison `i<f` control | 867 / 5 / 0 | unchanged |
| Guarded comparisons `i!<f i<f!` control | 1,930 / 11 / 0 | unchanged |
| Distinct JSON pair `choice=other` control | 3,649 / 16 / 2 | unchanged |
| Generic `Any` pair `data=data` control | 8,270 / 26 / 4 | unchanged |

Plain `EXPLAIN (COSTS OFF)` on the empty temporary-table fixture showed
Nested Loop and `Result` node counts changing from 11 to 7 and 12 to 8 for
`i<f i!=f f>i`, from 9 to 8 and 10 to 9 for `i<f j<f`, from 12 to 7 and 14
to 9 for `choice=choice`, and from 16 to 11 and 18 to 13 for
`items:any(x=>x=x)`. The single, distinct-pair, and `Any` controls were
unchanged. Plain-plan structure is fixture-specific; no `EXPLAIN ANALYZE`,
timing, or benchmark was collected.

## Correctness and tradeoffs

The integration fixture passed 16,416 local/PostgreSQL result comparisons on
both the pinned baseline and candidate across JSON and JSONB at Float settings
1 and 3. Three additional cache-key cases verified that equal input text does
not share across different ambiguity requirements, Float overrides, or
predecoded Float values. Existing dynamic pair, schema dispatch, arithmetic,
scope, and Float controls passed. Unit tests, `go vet`, integration-tagged
vet, SQL race checks, and independent review passed.

The generated SQL and sampled plan trees shrink for repeated kernels, but this
does not establish lower planning or execution time. These caches retain only
scope-local projections and must not merge work across callback or filter
boundaries.

## Decision and limits

Reuse repeated numeric comparison work only when the typed SQL expressions and
representation requirements match within the existing projection scope.
Further reuse of constructed containers, normalized JSON, or shared dispatch
requires its own representation and scope proofs.
