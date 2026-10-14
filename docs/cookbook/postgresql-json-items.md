# Select Tagged Values from JSON Object Entries

PostgreSQL can preserve the fixed key and value positions produced by
`object:items()` when the schema and stored JSON category identify the value
type. This recipe filters a JSONB map of nullable Bytes values by key and
decoded byte length.

```sql
CREATE TABLE artifacts (
    id bigint PRIMARY KEY,
    attachments jsonb
);

INSERT INTO artifacts VALUES
    (1, '{"release-manifest":"YQ=="}'),
    (2, '{"draft":"YQ=="}'),
    (3, '{"release-manifest":""}'),
    (4, '{"release-manifest":null}'),
    (5, 'null');
```

Declare `attachments` as `Map<Bytes|null>|Null`, bind it to the JSONB column,
and compile:

```hff
attachments:items:any(item=>item[0]:startswith("release-") item[1]:len>0)
```

Only row 1 matches: its key starts with `release-` and its decoded Bytes value
is nonempty. The `items()` callback receives `[String key, Bytes|null value]`;
the backend retains those roles through the literal tuple indexes. JSON stores
Bytes as canonical padded standard-base64 text, so `"YQ=="` represents the
single byte `a`. A stored null remains an ordinary null value and does not
match `len>0`.

Run the [example program](https://github.com/ornew/hff/tree/main/examples/sql-json-items)
from the repository root with:

```sh
go run ./examples/sql-json-items
```

It prints the generated PostgreSQL predicate and arguments, then the Local
results for the same records. Building the SQL does not connect to a database.

The backend preserves this positional information when the source `items()`
result and schema establish the key/value roles. Constant indexes `0` and
`-2` select the String key; `1` and `-1` select the value. Identity `map()` and
pair-only `flatmap()` callbacks can preserve those roles, including when they
duplicate pairs. Dynamic indexes that need tagged-type dispatch,
same-JSON-category alternatives whose identity cannot be recovered, and `Any`
strings requiring Bytes identity remain unsupported. Missing keys, stored
null, and guard-produced no-value retain their separate HFF behavior. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#bytes-in-json-documents)
for the supported subset and its limits.

For a non-null map, these queries show identity preservation, duplicate
detection, and first-occurrence deduplication:

```hff
attachments:items:map(pair=>pair)=attachments:items
attachments:items:flatmap(pair=>[pair,pair]):unique
attachments:items:flatmap(pair=>[pair,pair]):distinct=attachments:items
```

The first query compares equal to the source. The second returns false when
the source has at least one entry because the transformed list duplicates
every pair; for an empty source it returns true. A null source makes these
collection methods return null; the equality comparisons and final filter
truthiness return false. `unique()` returns a Bool reporting
whether the list already has no duplicates. `distinct()` instead returns the
first-occurrence list, so the third query compares equal to the source. Role
proofs retain type information through duplication but do not imply
uniqueness. Folds or other callback results without a complete pair-role
invariant retain the general representation path.

Folds can retain the same pair roles when the seed and every possible
accumulator result maintain one compatible pair or pair-list shape:

```hff
attachments:items:foldl(null,(acc,pair)=>pair):json=attachments:items[-1]:json
attachments:items:foldr(null,(pair,acc)=>pair):json=attachments:items[0]:json
attachments:items:foldl([], (acc,pair)=>acc:concat([pair])):json=attachments:items:json
attachments:items:foldr([], (pair,acc)=>[pair]:concat(acc)):json=attachments:items:json
```

For a nonempty map, the first two queries select the last and first pair
respectively. The latter two build a list of pairs in source key order. On an
empty list, a fold returns its seed (`null` or `[]` in these examples). An
ordinary null result becomes the next accumulator; it does not skip an item.
A reached guarded no-value still propagates through the entire fold. `concat()`
keeps pair-list roles when both operands have compatible provenance; an empty
list or null operand may be neutral for this proof only. A null concat operand
still makes the runtime result null. If the seed, callback result, or combined
list can originate from an ambiguous schema such as String versus Duration,
the backend retains general lowering and may reject operations that require the
unrecoverable tagged identity. Pair-role provenance never changes the public
type or capability contract.
