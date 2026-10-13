# 022. Avoid JSON round-trips in scalar JSON pair comparisons

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

Comparing two JSON-backed dynamic scalar values could serialize each Float to
canonical JSON and decode it again before comparison. This change consumes the
original JSON value, or the value restored from JSONB metadata, and decodes
each reachable numeric family once. It keeps stored-position schema checks,
JSONB metadata recovery, and unsupported tagged-union checks ahead of the
scalar path. Comparisons with reachable containers retain normalized
recursive structural equality.

The change reduces generated SQL. It does not change HFF comparison semantics,
stored-value validation requirements, or JSONB metadata requirements.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Dynamic scalar pairs with no shared container family | Applied | Compare original JSON or restored scalar values without canonical JSON serialization. |
| Scalar pairs with reachable numeric alternatives | Applied | Decode each permitted Int/Float family once and retain exact mixed-numeric comparison. |
| Equality pairs with a shared reachable list or object family | Not applied | The normalized recursive structural-equality path remains in use. |
| Ordering pairs whose types also permit containers | Applied | Ordering has no structural branch and uses the scalar path without document normalization. |
| Stored alternatives requiring JSONB metadata or schema validation | Applied after existing checks | Stored-value compatibility, metadata recovery, and representability checks still run first. |

## Reproduction

The SQL-shape baseline is commit [9104dda](https://github.com/ornew/hff/commit/9104dda33658b3ee11dcf60989821ddd01c95e0d); its SQL code matches
414a1a2. The candidate implementation accompanies this record. The focused
fixtures bind JSONB documents without numeric metadata where each schema fixes
the numeric subtype:

| Binding | Compiled type | Query |
|:--|:--|:--|
| `records.choice` | `String|Int` | `choice=choice` |
| `records.fchoice` | `String|Float` | `fchoice=fchoice` |
| `records.fchoice`, `records.choice` | `String|Float`, `String|Int` | `fchoice<choice` |

Shape tests are in `backends/sql/scalar_pair_decode_test.go`; physical-spelling
parity cases are in `backends/sql/scalar_pair_decode_integration_test.go`.
The parity fixture compares SQL results with local evaluation in JSON and
JSONB, including fixed Float spellings above int64, decimal/exponent Int
spellings, underflow, mixed numeric comparisons, and type guards.

The focused checks included:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run 'TestScalarPairs(AvoidFloatSerialization|ValidateMergedStoredPositions)$' -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLScalarPairPhysicalSpellings$' -count=1
```

Existing controls included the 2,016-case JSON/JSONB dynamic-pair matrix,
schema-known pair comparisons, tagged-union rejection, and JSON Float underflow
checks. The new physical-spelling test adds 208 JSON/JSONB comparisons.

For a plain plan comparison, the `fchoice=fchoice` JSONB fixture contains
String, Float, and SQL NULL rows. `EXPLAIN (COSTS OFF)` was inspected without
`ANALYZE`; the captured plans are outside the repository at
`/private/tmp/hff-scalar-pair-{baseline,candidate}.plan`.

## Results

| HFF filter and schema | Baseline `WHERE` characters / barriers | Candidate characters / barriers |
|:--|--:|--:|
| `choice=choice`, `String|Int` | 1,733 / 6 | 1,069 / 4 |
| `fchoice=fchoice`, `String|Float` | 48,744 / 108 | 6,570 / 16 |
| `fchoice<choice`, `String|Float` vs `String|Int` | 45,972 / 103 | 4,907 / 18 |

For the Float self-comparison plan, the baseline had 234 plan lines, 93 Nested
Loop nodes, four `generate_series` Function Scans, and 603,067 bytes of verbose
expression output. The candidate had 34 lines, 13 Nested Loop nodes, no
`generate_series` scan, and 1,492 bytes. These are fixture-specific plan
structures from `EXPLAIN (COSTS OFF)`, not execution measurements. No benchmark,
`EXPLAIN ANALYZE`, or timing was collected.

## Correctness and tradeoffs

The new parity cases cover 208 physical-spelling comparisons across JSON and
JSONB. They include values whose stored spelling does not reveal the
schema-selected subtype, finite underflow to signed zero, and exact mixed
Int/Float comparisons. Existing 2,016-case dynamic-pair coverage and
schema-known/tagged-union/underflow controls also passed. Stored Float values
remain subject to the caller's finite-value schema contract; malformed or
schema-incompatible rows can fail during database evaluation. Checked text
conversion has a separate null-on-overflow contract.

Schema checks still visit merged stored positions before scalar comparison,
including alternatives not active in the current row. Unused object-key
validation does not bind unused key arguments. Structural equality remains on
the generic path when a matching list/object family is reachable.

## Decision and limits

Keep scalar pairs on the original stored representation after existing
schema and metadata checks, while retaining the recursive path for structural
comparisons. The observed SQL and plan reductions do not establish runtime
speed or plan selection for other schemas, rows, or PostgreSQL configurations.
