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

The backend preserves these positional roles when the source `items()` result
and schema establish them. Constant indexes `0` and `-2` select the String
key; `1` and `-1` select the value. Identity `map()` and pair-only `flatmap()`
callbacks can preserve the roles, including when they duplicate pairs. Same-
category alternatives whose identity cannot be recovered and `Any` strings
requiring Bytes identity remain unsupported. Missing keys, stored null, and
guard-produced no-value retain their separate HFF behavior. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#bytes-in-json-documents)
for the supported subset and its limits.

## Keep `Any` strings distinct from tagged fields

Per-key schema identity also lets an `Any` field sit beside fields with tagged
string representations. In this example `app` is `Any|null`, while the other
fields retain their declared types:

```sql
CREATE TABLE events (
    id bigint PRIMARY KEY,
    labels jsonb,
    labels_meta jsonb NOT NULL DEFAULT '{"version":1,"types":{}}'::jsonb
);

INSERT INTO events (id, labels) VALUES
    (1, '{"app":"YQ==","asset":"YQ==","elapsed":"0.000000001s","recorded":"2026-10-15T00:00:00Z"}'),
    (2, '{"app":"2026-10-15T00:00:00Z","asset":"Yg==","elapsed":"1s","recorded":"2026-10-15T00:00:01Z"}'),
    (3, '{"app":"","asset":null,"elapsed":"2s","recorded":"2026-10-15T00:00:02Z"}'),
    (4, '{"app":null,"asset":null,"elapsed":null,"recorded":null}');
```

Use this schema and binding (with the usual engine and PostgreSQL setup):

```go
schema.Struct(schema.Nullable(schema.Field("labels", schema.Struct(
	schema.Nullable(schema.Field("app", schema.AnyType)),
	schema.Nullable(schema.Bytes("asset")),
	schema.Nullable(schema.Duration("elapsed")),
	schema.Nullable(schema.Timestamp("recorded")),
))))

hffsql.BindJSON(
	hff.Path{"labels"},
	hffsql.Column("events", "labels"),
	hffsql.JSONB,
	hffsql.WithTypeMetadata(hffsql.Column("events", "labels_meta")),
)
```

The empty version-1 metadata is valid for these string and null values. If
`app` contains numbers, populate its metadata with their numeric subtypes;
an empty `types` map cannot restore those subtypes from JSONB. The first two
queries inspect the `app` value as `Any`, then narrow it to String. The third
observes the separate declared Bytes field; it checks its HFF type, not its
length:

```hff
labels:items:any(pair=>pair[0]="app" pair[1]:[any]:type="string")
labels:items:any(pair=>pair[0]="app" pair[1]:[any]:[string]:len>0)
labels:items:any(pair=>pair[0]="asset" pair[1]:[any]:type="bytes")
```

The first matches rows 1–3 because their non-null `app` values are strings.
The second matches rows 1 and 2: the base64-looking and Timestamp-looking
texts in `Any` are still Strings, while the empty string in row 3 has length
zero. The third matches rows 1 and 2 because their `asset` fields are declared
Bytes; row 4's null field does not match. An `Any` string does not
acquire Bytes, Duration, or Timestamp identity from its spelling; identity
comes from the separate field's schema.

## Continue a selected slot through a map chain

A direct map/filter sequence can retain each selected value's field identity
through later maps and predicates. With the schema and rows above, these
queries inspect the type of each `items()` value:

```hff
labels:items:map(pair=>pair[1]):map(value=>value:type):contains("bytes")
labels:items:map(pair=>pair[1]):any(value=>value:type="bytes")
labels:items:map(pair=>pair[1]):count(value=>value:type="string")=1
labels:items:map(pair=>pair[1]!):any(value=>true)
```

The first two match rows 1 and 2, whose `asset` values are Bytes. The count
query matches rows 1–3, each of which has one String in its `app` field. The
last query matches rows 1 and 2; row 3's null `asset` value makes the earlier
map produce no-value, so the later constant-true predicate cannot hide it.
This pipeline keeps the selected slot's schema through direct map/filter
sequences. Filters can remove rows before later maps, but their guards do not
change the retained value's type. An earlier map's no-value remains a failure
even if a later filter would remove that row. The eligible concat and bounded
`flatmap()` compositions described below can also carry these slots to a
consumer. Other materialization boundaries and folds without a closed slot
provenance remain outside this support.

For example, filtering by observed type removes null payloads before the next
map, while retaining the original field roles:

```hff
labels:items:map(pair=>pair[1]):filter(value=>value:type="bytes"):map(value=>value:type):contains("bytes")
labels:items:map(pair=>pair[1]):filter(value=>value:exists):map(value=>value!):count(value=>true)=3
labels:items:map(pair=>pair[1]!):filter(value=>false):all(value=>true)
```

The first matches rows 1 and 2, whose `asset` field is Bytes. The second
matches row 3: filtering removes its null `asset`, and the three remaining
values are `app`, `elapsed`, and `recorded`. The third matches rows 1 and 2,
whose mapped values contain no nulls. For rows 3 and 4, the first map's `!`
produces no-value; the later always-false filter cannot erase that failure.

### Concatenate deferred branches for an immediate consumer

Eligible deferred `items()` branches can be directly concatenated and consumed
by `map()`, `any()`, `all()`, or `count(predicate)`. This immediate map keeps
the branch order visible in the resulting type list:

```hff
labels:items:map(pair=>pair[1]):concat(labels:items:map(pair=>pair[1])):map(value=>value:type)=["string","bytes","duration","timestamp","string","bytes","duration","timestamp"]
```

Rows 1 and 2 match.

### Continue a shared pipeline after concatenating deferred branches

An eligible direct concat can continue through an elementwise map/filter chain
and then a terminal `map()`, `any()`, `all()`, or `count(predicate)`. Each
branch keeps its own key and schema while callbacks resolve the selected value,
and nested concat leaves preserve branch order. With the `events` schema and
rows above, this query filters nulls and maps each remaining value to its HFF
type name:

```hff
labels:items:map(pair=>pair[1]):concat(labels:items:map(pair=>pair[1])):filter(value=>value:type!="null"):map(value=>value:[any]):map(value=>value:type)=["string","bytes","duration","timestamp","string","bytes","duration","timestamp"]
```

Rows 1 and 2 match: each branch contributes `string`, `bytes`, `duration`,
`timestamp` in object-key order, and the branches remain left-to-right. Complete
item pairs can also be concatenated before a later map selects a slot. For a
deferred slot, `json()` observes and serializes each value using that key's
schema. Returning an unresolved ambiguous value directly from the terminal
`map()` still fails type recovery. A no-value from a map before concat remains
a failure even if later filtering excludes that row. If concat returns null,
it masks failures from all later maps; filters do not narrow their retained
values, and ordinary null remains data.

A map may also appear between two concat levels. The inner mapped values remain
recoverable within the enclosing tree, while the final map emits ordinary type
names:

```hff
labels:items:map(pair=>pair[1]):concat(labels:items:map(pair=>pair[1])):map(value=>value:[any]):concat(labels:items:map(pair=>pair[1])):map(value=>value:type)=["string","bytes","duration","timestamp","string","bytes","duration","timestamp","string","bytes","duration","timestamp"]
```

Rows 1 and 2 match. The values from the first concat level stay ahead of the
third deferred branch, and each branch keeps its own item-slot schema.

The same item-slot path supports bounded `flatmap()` composition from a list
literal. This query duplicates each selected value before mapping it to its
type name:

```hff
labels:items:map(pair=>pair[1]):flatmap(value=>[value,value]):map(value=>value:type)=["string","string","bytes","bytes","duration","duration","timestamp","timestamp"]
```

Rows 1 and 2 match. Each object's values remain in key order, and every value
appears twice. Within this path, list literals, maps, filters, concat trees,
nested `flatmap()` calls, and recoverable list expressions can be composed.
A callback that returns a non-list makes the whole `flatmap()` result null;
guarded no-value propagates no-value. The first invalid callback result
determines the result, even if later callbacks return an empty list. Ordinary
null inside a returned list remains data. Fold carriers and unrelated
materialization boundaries remain outside this support.

### Wrap and recover constructed values

Constructed lists and objects can retain the selected value's type through
later operations. These queries compare the wrapped result with a direct type
observation; all four example rows match, including rows with null values:

```hff
labels:items:map(pair=>pair[1]):map(value=>[value]):flatmap(values=>values):map(value=>value:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json
labels:items:map(pair=>pair[1]):map(value=>{"payload":value}):map(object=>object:get("payload"):type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json
labels:items:map(pair=>pair[1]):map(value=>{"payload":value}):map(object=>object.payload:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json
labels:items:map(pair=>pair[1]):map(value=>[value,value]):map(values=>values[-1]:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json
labels:items:map(pair=>pair[1]):map(value=>[value]):map(values=>values:[list[any]]):flatmap(values=>values):map(value=>value:type):json=labels:items:map(pair=>pair[1]):map(value=>value:type):json
labels:items:map(pair=>pair[1]):map(value=>[value,null]):map(values=>values:size):all(size=>size=2)
```

These examples cover a list wrapper followed by `flatmap()`, object field
access through `get()`, literal-list indexing and type guards, and the `size()`
observer. The wrapper preserves each field's declared type, and ordinary null
remains a value inside the constructed collection. A guard-produced no-value
still propagates instead of being silently removed.

A later filter still cannot erase a no-value produced by an earlier map:

```hff
labels:items:map(pair=>pair[1]):concat(labels:items:map(pair=>pair[1])):map(value=>value!):filter(value=>false):all(value=>true)
```

Rows 1 and 2 match. Rows 3 and 4 have null values that make the earlier map
produce no-value; the later always-false filter does not erase that failure.

### Index deferred list results

An eligible list produced by a deferred item pipeline can be indexed before
its values are materialized. Indexes count retained outputs in full order,
after filters and `flatmap()` expansion:

```hff
labels:items:map(pair=>pair[1]):flatmap(value=>[value])[0]:type="string"
labels:items:map(pair=>pair[1]):flatmap(value=>[value])[1]:type="bytes"
labels:items:map(pair=>pair[1]):filter(value=>value:exists):flatmap(value=>[value])[labels:size%3-1]:type="string"
labels:items:map(pair=>pair[1]!):flatmap(value=>[value])[0]:type="string"
```

The first query matches rows 1–3, whose first value is the String `app` field;
the second matches rows 1 and 2, whose second value is Bytes. The third uses a
dynamic integral index. The object has four fields, so `labels:size%3-1` is
zero: rows 1–3 match on their first retained String, while row 4 has no values
after the filter. The last query matches rows 1 and 2. Rows 3 and 4 still fail
because the whole-list map encounters null values and produces no-value, even
though the selected index is zero. Out-of-range and fractional indexes remain
ordinary null; a statically incompatible index remains a compile error.

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
