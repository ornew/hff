# 052. Traverse object keys directly for key-only `items()` consumers

- **Status**: Implemented
- **Date**: 2026-10-15

## Problem and mechanism

PostgreSQL `object:items()` normally builds ordered `[key, value]` pairs after
normalizing object values. A consumer that observes only each key does not
need those values or the pair arrays. For eligible key-only consumers, the
translator can scan `json_object_keys` or `jsonb_object_keys` directly and
lower the callback against the key text.

For an otherwise supported source expression, the proof follows lexical
callback binding IDs and fixed key-slot access
(`0`/`-2`), including integral-valued literal indexes. It permits key
comparisons and key type guards, nested captures, and shadowed callback
variables. Ordered operations retain the existing Unicode code-point `C` key
order. Source expressions are still evaluated, and null, no-value, map, and
flatmap rules are unchanged. Unobserved values do not need JSONB numeric
metadata or tagged-subtype recovery.

## Applicability

| Consumer | Applied / Partial / Not applied | Reason |
|:--|:--|:--|
| `items:any()`/`all()`/`count(predicate)` reading only the fixed key slot | Applied | The callback does not inspect the value or complete pair. |
| `items:map()`/`flatmap()` observing only the key | Applied | Key values can feed the consumer without restoring pair values. |
| `sum()`/`avg()`/`min()`/`max()` projections of the key | Applied | The projection reads only the String key. |
| `size()`/`count()` | Applied | The key scan supplies the entry count without constructing pairs. |
| Key comparisons and key type guards | Applied | They depend on the key text only. |
| Full-pair or list type guards, value-slot reads, or dynamic indexes | Not applied | These require pair shape or value-dependent type dispatch. |
| `filter()`/`find()` returning pairs | Not applied | Their result contains the original pair values. |

## Reproduction

The baseline is [5c3fc61](https://github.com/ornew/hff/commit/5c3fc614864838545f07cdb60bfb65bc9e8f3627).
Use Go 1.27.1 and a nullable optional `Map<Bytes|null>` field `labels` bound
to native JSON or JSONB without numeric metadata. The SQL-shape observations
compare `labels:items:any(p=>p[0]="app")` and
`labels:items:map(p=>p[0]):json=keys_json` on both formats, with `keys_json`
bound as String to `records.keys_json`. The primary PostgreSQL/Local parity
matrix covers 29 source forms across 20
schema/format configurations. A second no-metadata matrix covers 23 key-only
forms across six additional configurations: `Any`, `Map<Int|Float|null>`,
and `Map<String|Bytes|null>` in JSON and JSONB. Together they verify 8,214
outputs, including constructed-object guards, captured size predicates,
generic fallbacks, and accepted cases that do not observe values.

Run the focused checks with:

```sh
go test ./backends/sql -run '^TestJSONItemKeys' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLJSONItemKeysMatchLocal$' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLJSONItemKeysWithoutMetadata$' -count=1
HFF_POSTGRES_CONTAINER=<container> go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLJSONItemKeysKeepOrderAndFirstFailure$' -count=1
```

Raw SQL-shape and parity logs are retained at
`/private/tmp/hff-item-keys-{baseline,candidate,parity,unit}.log`.
The supplemental ordering and first-failure log is
`/private/tmp/hff-item-keys-order.log`.

## Results

| Workload | Baseline `WHERE` bytes | Candidate `WHERE` bytes | Change | Uncertainty |
|:--|--:|--:|--:|:--|
| `labels:items:any(p=>p[0]="app")`, JSON | 2,909 | 941 | −1,968 bytes | SQL shape only |
| `labels:items:any(p=>p[0]="app")`, JSONB | 2,915 | 945 | −1,970 bytes | SQL shape only |
| `labels:items:map(p=>p[0]):json=keys_json`, JSON | 3,730 | 1,841 | −1,889 bytes | SQL shape only |
| `labels:items:map(p=>p[0]):json=keys_json`, JSONB | 3,736 | 1,845 | −1,891 bytes | SQL shape only |

The parity matrices covered 8,214 PostgreSQL/Local outputs: 29 source forms
across 20 schema/format configurations, plus 23 no-metadata forms across six
additional configurations. These observations measure generated SQL size,
not execution time, allocation, or retained memory. Benchmarks and
`EXPLAIN ANALYZE` were not run.

An additional ordering and first-failure matrix passed 306 outputs: nine
queries over 17 records in both formats. It checks encoded JSON key order,
null versus no-value on the first failing entry, and Int/Float equal-value
selection under C key order even when JSONB physical key order differs. The
primary parity total above excludes these supplemental cases.
The combined focused total is 8,520 outputs.

## Correctness and tradeoffs

The key-only path applies only after the source expression is otherwise
supported. It still evaluates that expression and preserves its state. It
avoids decoding values that the consumer never observes; it does
not bypass schema checks for observed keys or change callback evaluation
rules. Consumers that inspect a value, complete pair, or pair/list shape keep
the existing pair-construction and normalization path.

## Decision and limits

Use direct object-key traversal only when the expression proves fixed key-slot
observation. Keep generic lowering for value-dependent and pair-returning
consumers. SQL-size changes do not establish runtime speed.
