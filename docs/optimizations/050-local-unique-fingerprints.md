# 050. Narrow Local `unique()` comparisons with fingerprints

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Local `list:unique()` previously compared each item with every earlier item.
It now preserves the short-list paths, then fingerprints each item and compares
it only with prior items in the same fingerprint bucket. Fingerprints are
candidate selectors, never proof of equality: each candidate is confirmed by
the existing structural HFF equality operation. The helper is shared with
Local `distinct()`.

The scan remains incremental. Empty and singleton lists return true, two-item
lists use the existing direct comparison, and an equal first pair returns
false before hashing or reading later elements. For larger lists, the
implementation fingerprints and checks each element in order, returning false
as soon as an equal prior candidate is found. Cancellation and MaxSteps
accounting apply during fingerprint construction and collision checks. The
boolean operation creates no generated collection result and adds no
MaxGeneratedValues charge.

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Local `list:unique()` | Applied | Fingerprints narrow candidate comparisons; HFF equality verifies each candidate. |
| Local `list:distinct()` | Existing mechanism | It already uses the same fingerprint buckets and collision-confirmed equality. |
| Other backends | Not changed | Their equality and uniqueness lowering is unaffected. |

## Reproduction

The baseline is [9731d3e](https://github.com/ornew/hff/commit/9731d3e973daef4825af3aeed8043b38d78fa284).
The candidate is the commit containing this record and the implementation.
Go 1.27.1 was used. The deterministic work ledger does not depend on hardware
and is not a timing measurement.

Run the Local-focused candidate tests and full checks:

```sh
go test ./backends/local -run 'TestUnique|TestFingerprintCollisionRequiresEquality' -count=1 -v
go test ./...
go vet ./...
```

`backends/local/unique_kernel_test.go` compares the fingerprint path with a
pairwise HFF-equality oracle over 1,296 value pairs and 300 deterministic random
list matrices. It also forces collisions, checks numeric precision boundaries,
and records isolated work counts. `backends/local/unique_test.go` covers the
public result, early duplicates, a late mixed-numeric duplicate, work limits,
and cancellation. The same 14 public semantic cases and cancellation checks
were run on the pinned baseline using a copied public test fixture; raw logs
are retained at `/private/tmp/hff-unique-fingerprint-*`. Its pairwise work
exceeds the candidate's 1,000-step limit for 256 distinct integers, while the
fingerprint path passes with `MaxSteps: 1000` and `MaxGeneratedValues: 1`.

## Results

The isolated kernel test counted charged evaluation steps for lists of distinct
Int values. The candidate retains the same fast-path counts for zero, one, or
two values;
larger lists visit each value once after the initial pair comparison:

| Distinct Int count | Pairwise oracle steps | Fingerprint steps |
|--:|--:|--:|
| 0 | 0 | 0 |
| 1 | 0 | 0 |
| 2 | 1 | 1 |
| 16 | 120 | 17 |
| 128 | 8,128 | 129 |
| 512 | 130,816 | 513 |

These are isolated algorithm-ledger counts from the unit test. They do not
include whole-match validation or the other query-evaluation charges, and they
are not runtime measurements. The implementation uses auxiliary bucket and
index storage. Hash collisions can still require many equality checks, so
worst-case linear work is not guaranteed; those checks remain cancellable and
budgeted.

## Correctness and tradeoffs

The fingerprint must assign equal values to the same bucket. It normalizes
equal Int and Float numeric values without rounding large Ints through Float;
recursive List values preserve element order, while Objects use their actual
key sets in deterministic key order. Nulls, tagged scalar values, nested
containers, and numeric boundaries remain subject to the same HFF equality
confirmation as before. Unequal values may share a bucket without changing
the result because `e.equal` resolves every collision.

The input is processed in order and the first duplicate still returns false
without examining the remainder. Fingerprint traversal and equality both
charge MaxSteps and observe context cancellation. `unique()` remains a
boolean-only operation and does not charge generated output values. The
candidate map adds temporary memory proportional to indexed values and
collision buckets.

## Decision and limits

Use deterministic fingerprints to narrow Local `unique()` candidate checks,
with HFF equality as the final authority. Keep the small-list fast paths,
streaming early exit, cancellation, and work-budget checks. The work-count
results describe the isolated kernel only; they do not establish runtime speed
or a linear worst-case bound.
