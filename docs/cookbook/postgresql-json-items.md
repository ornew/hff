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

## Guard recovered pair shapes

The fixed roles also let a type guard inspect a pair without treating its two
positions as one undifferentiated union. With the `Map<Bytes|null>` binding
above, these guards distinguish the String key from the Bytes-or-null payload:

```hff
attachments:items[0]:[list[string]]:exists
attachments:items[0]:[list[bytes]]:exists
attachments:items:[list[list[string]]]:exists
```

The first guard is false for a Bytes payload and for a null payload; the second
is false because the key is String. The outer guard is false for a nonempty
map, since each pair contains a Bytes-or-null value, and true for an empty map.
If the binding instead declares `Map<String|null>`, the pair guard
`attachments:items[0]:[list[string|null]]:exists` admits a null payload, while
`[list[string]]` does not. Guard results for null receivers follow the target
type, and no-value remains distinct from ordinary null. These checks require
the same recoverable source schema as pair-slot access; they do not make
ambiguous same-category alternatives recoverable.

## Observe a pair slot with a dynamic index

Pair callbacks can inspect a slot's type or existence, and can apply a type
guard before a typed operation. This query checks the key and value positions
using a list of valid numeric indexes:

```hff
attachments:items:any(pair=>pair[0]:type="string")
attachments:items:any(pair=>pair[1]:[bytes]:exists)
attachments:items:any(pair=>[0,1,-1,-2]:any(i=>pair[i]:[bytes]:len>0))
```

The first query matches rows 1–4, whose non-null maps contain String keys. The
second matches rows 1–3, whose values are Bytes; the null value in row 4 does
not match. The third matches rows 1 and 2 because at least one of the key/value
indexes selects nonempty Bytes. Integer-valued Float indexes and negative
indexes are also valid. Fractional and out-of-range numbers produce ordinary
null, as do nonnumeric runtime values of a union or `Any` index. Statically
incompatible index types remain compile errors. If evaluating the index
produces guarded no-value, that state remains distinct; a null pair receiver
masks a later index guard.

Consecutive guards can inspect and narrow an ambiguous value slot while
retaining its original per-key role:

```hff
attachments:items:any(pair=>pair[1]:[string|bytes|null]:type="bytes")
attachments:items:any(pair=>pair[1]:[string|bytes|null]:[bytes]:len>0)
attachments:items:any(pair=>pair[1]:[null]:[any]:type="null")
```

The first matches rows 1–3, where the payload is Bytes. The second narrows the
same slot to Bytes before checking its length and matches rows 1 and 2. The
third matches only row 4, whose payload is null. A failed guard produces
no-value, which a later null-accepting guard cannot revive. `exists()` reports
false and `type()` remains no-value after that failure. These observation-local
chains do not make an ambiguous slot safe to return as a general value; the
source schema and representation must still recover the original key/value
roles.

The backend preserves this positional information when the source `items()`
result and schema establish the key/value roles. Constant indexes `0` and
`-2` select the String key; `1` and `-1` select the value. Identity `map()` and
pair-only `flatmap()` callbacks can preserve those roles, including when they
duplicate pairs. Same-JSON-category alternatives whose identity cannot be
recovered and `Any` strings requiring Bytes identity remain unsupported.
Missing keys, stored null, and guard-produced no-value retain their separate
HFF behavior. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#bytes-in-json-documents)
for the supported subset and its limits.

## Consume object keys without reading values

When an otherwise supported `items()` consumer uses only the fixed key slot,
PostgreSQL can traverse the object's keys directly without decoding values or
constructing `[key, value]` arrays. For example, with `attachments` bound as
`Map<Bytes|null>|Null`, this query checks whether any key starts with the
release prefix:

```hff
attachments:items:any(pair=>pair[0]:startswith("release-"))
```

Rows 1, 3, and 4 match because their keys start with `release-`, including the
rows whose Bytes value is empty or null. The key text is available without
decoding those values. The source expression still runs, and null/no-value
behavior is unchanged. This path requires a fixed key-slot access (`0` or
`-2`) and does not apply when the callback reads the value, observes the full
pair/list shape, or returns pairs.
See the [key-only traversal record](../optimizations/052-postgresql-direct-object-item-keys.md)
for the full eligibility boundary and SQL-shape evidence.

## Preserve and combine complete pairs

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
