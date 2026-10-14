# 051. Skip pairwise checks for unique object-item keys

- **Status**: Implemented
- **Date**: 2026-10-15

## Problem and mechanism

`items():unique()` over a recovered object receiver produces one
`[key, value]` pair for each object key. Under the PostgreSQL JSON storage
contract, object keys are unique, and different source pairs therefore cannot
be equal: their String keys differ. The translator can preserve a private
one-entry-per-source-key proof and omit the pairwise duplicate comparison
while still lowering and evaluating the receiver. Receiver null/no-value
behavior, schema checks, and compile-time capability checks remain in force.

The proof applies to direct `items()`, guards, and transformations that only
retain or preserve one pair for each input: `filter()`, a lexical identity
`map()`, and a singleton identity `flatmap()`. Separately, `distinct()` proves
that its output contains no equal pairs. This pair-equality proof applies even
when the input could repeat a key with different values; it is not a
one-entry-per-key proof. Pair-role provenance alone is not enough. A transform
that can duplicate, capture, combine, or reconstruct entries keeps the
general comparison unless a later operation establishes one of these proofs.
This includes duplication-capable `map()` or `flatmap()`, concatenation, and
folds without a distinctness proof.

## Applicability

| Input list | Applied / Not applied | Reason |
|:--|:--|:--|
| Recovered object `items()` receiver, including an eligible `Any`/union branch | Applied | The stored-object contract gives one entry per key. |
| Guarded, filtered, or identity-preserving item list | Applied | These paths retain at most one pair per source key. |
| `distinct()` over proven item lists | Applied | Distinctness proves there are no equal pairs, even if different values share a key. |
| Lexical identity `map()` or singleton identity `flatmap()` | Applied | Each input pair contributes exactly one unchanged pair. |
| Pair-role-preserving transformation that may duplicate or reconstruct entries | Not applied | Pair roles do not prove one entry per key. |
| Concatenated or folded list without a separate uniqueness proof | Not applied | The result may contain repeated keys. |

## Reproduction

The baseline is [be6d7c6](https://github.com/ornew/hff/commit/be6d7c6ba0722be1f871b2c2d29bf0143c0531d3).
The candidate is the revision containing this implementation and record. Use
Go 1.27.1 and build `labels:items:unique` with an optional `labels` field of
type `Map<Bytes|null>|Null`, bound to `records.labels` as native JSON or JSONB
with no numeric metadata. The SQL-shape checks use 21 query forms in this one
schema with both formats. The focused PostgreSQL/local parity test covered
4,764 results across 20 schema/format configurations. Run the candidate checks
with:

```sh
go test ./backends/sql -run '^TestJSONItemUniqueness' -count=1 -v
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLJSONItemUniquenessMatchLocal$' -count=1
```

Compare the UTF-8 byte length of `Query.Where()` for each format. Raw Build
observations are retained at
`/private/tmp/hff-item-unique-baseline.log` and
`/private/tmp/hff-item-unique-candidate.log`.

The stored-object contract requires unique object keys for both JSON formats.
PostgreSQL `json` can physically contain duplicate keys, but `Build` does not
inspect bound rows; callers must honor the storage contract. This optimization
does not make malformed duplicate-key documents safe.

## Results

| Workload | Baseline `WHERE` bytes | Candidate `WHERE` bytes | Change | Uncertainty |
|:--|--:|--:|--:|:--|
| `labels:items:unique`, JSON | 3,166 | 2,100 | −1,066 bytes | SQL shape only |
| `labels:items:unique`, JSONB | 3,172 | 2,106 | −1,066 bytes | SQL shape only |

The omitted work is the pairwise array cross-product. Receiver normalization
still occurs. These byte counts do not measure database execution time,
allocation, or memory use; benchmarks and `EXPLAIN ANALYZE` were not run.

## Correctness and tradeoffs

The source-key shortcut depends on the caller's valid-object storage contract
and a separate proof that transformations retain at most one entry per key.
The distinct-output shortcut uses pair-equality uniqueness instead. Neither
proof bypasses receiver evaluation, schema validation, or capability
checking. When neither proof is available, retaining the existing HFF equality
path preserves the general behavior.

## Decision and limits

Use only the source-key or pair-equality proof described above. Do not infer
either proof from pair-role provenance alone. The results describe generated
SQL shape only and do not establish runtime speed.
