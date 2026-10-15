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

## Project a recoverable field from an object union

A query can select a uniquely typed field from an object branch without
normalizing an unrelated field whose stored String representation is
ambiguous:

```sql
CREATE TABLE projection_events (
    id bigint PRIMARY KEY,
    labels jsonb NOT NULL
);

INSERT INTO projection_events VALUES
    (1, '{"app":"auth","opaque":"YQ=="}'),
    (2, '{"app":"billing","opaque":"Yg=="}'),
    (3, '0'),
    (4, '["YQ=="]');
```

Use a category-disjoint root union. The `opaque` field intentionally has
ambiguous String-or-Bytes identity; the `app` field remains a known String:

```go
objectType := schema.Struct(
	schema.String("app"),
	schema.Field("opaque", schema.Union(schema.StringType, schema.BytesType)),
)
labelsType := schema.Union(
	objectType,
	schema.IntType,
	schema.ListOf(schema.BytesType),
)
```

Configure the schema and binding, then build the first predicate:

```go
e, err := engine.New(
	engine.WithSchema(schema.Struct(schema.Field("labels", labelsType))),
	engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
	return err
}
program, err := e.Compile(ctx, `labels.app="auth"`)
if err != nil {
	return err
}
query, err := hffsql.Build(ctx, program,
	hffsql.WithDialect(hffsql.PostgreSQL),
	hffsql.WithBindings(hffsql.BindJSON(
		hff.Path{"labels"},
		hffsql.Column("projection_events", "labels"),
		hffsql.JSONB,
	)),
)
if err != nil {
	return err
}
```

Build the second predicate with the same binding. Both predicates match only
row 1:

```hff
labels.app="auth"
labels:get("app")="auth"
```

The root JSON category selects the object branch, and the selected `app`
field has a recoverable String type. Selecting `opaque` or serializing the
whole `labels` value still requires distinguishing String from Bytes and may
be rejected by `Build`. A String in an `Any` field also remains a String; its
spelling does not imply Bytes.

Root type inspection and shape guards can observe the category without
decoding the ambiguous `opaque` field. These queries use the same table,
schema, and binding:

```hff
labels:type="object"
labels:[object].app="auth"
labels:[map[any]]:get("app")="auth"
labels:[int]=0
labels:[list[bytes]]:size=1
```

The first matches rows 1 and 2. The second and third match only row 1; the
fourth matches row 3 and the fifth row 4. After an object guard, `app` retains
its declared String type. The shape checks do not make `opaque` recoverable,
and an `Any` string still does not imply Bytes. Operations that require
numeric distinctions lost by JSONB still need type metadata.

## Preserve field types in an object alternative

A schema-known object branch can retain each field's type even when the root
JSON value may instead be an integer, a byte list, or null:

```sql
CREATE TABLE typed_events (
    id integer PRIMARY KEY,
    labels jsonb
);

INSERT INTO typed_events VALUES
    (1, '{"app":"auth","asset":"YQ=="}'),
    (2, '{"app":"billing","asset":"Yg=="}'),
    (3, '0'),
    (4, '["YQ=="]');
```

Declare the alternatives by their JSON categories:

```go
objectType := schema.Struct(
	schema.String("app"),
	schema.Bytes("asset"),
)
labelsType := schema.NullableType(schema.Union(
	objectType,
	schema.IntType,
	schema.ListOf(schema.BytesType),
))

e, err := engine.New(
	engine.WithSchema(schema.Struct(schema.Field("labels", labelsType))),
	engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
	return err
}
```

Compile and build each query with `labels` bound to
`hffsql.Column("typed_events", "labels")` using `hffsql.BindJSON` and the
`hffsql.JSONB` format. These queries match rows 1 and 2:

```hff
labels:values:map(value=>value:type):json=["string","bytes"]:json
["app","asset"]:map(key=>labels:get(key)):map(value=>value:type):json=["string","bytes"]:json
```

The first enumerates the two declared fields. The second looks up fields with
keys produced at runtime and preserves each selected field's declared type.
Rows 3 and 4 have non-object roots, so the object operations return null and do
not match. A dynamic numeric index can also select a list alternative; this
query matches row 4 only:

```hff
[0]:any(key=>labels[key]:[bytes]:base64="YQ==")
```

The index expression is evaluated for every non-null root. An incompatible
root, a null index, or a wrong-family index returns ordinary null; an index
expression that produces no-value remains no-value. In contrast,
`get()` skips its key expression when the root is not an object. This relies on
the declared, category-disjoint alternatives and does not recover an
ambiguous field or infer a tagged type from a string inside `Any`.

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

### Preserve types in explicit constructor children

An explicit list constructor keeps the declared type of each child at its
position. These queries combine the Bytes `asset` and Duration `elapsed`
fields, then inspect, map, or search the constructed list:

```hff
[labels.asset,labels.elapsed][0]:type="bytes"
[labels.asset,labels.elapsed]:map(value=>value:type):json=["bytes","duration"]:json
labels.asset:exists [labels.asset,labels.elapsed]:contains(labels.asset)
```

All three match rows 1 and 2, where `asset` is non-null Bytes and `elapsed` is
Duration. Rows 3 and 4 have a null `asset` and do not match. The types come
from the two declared fields, not from their JSON string spellings. This
bounded support does not infer tagged identity from an `Any` string or make an
ambiguous stored union recoverable.

### Select a declared field with a dynamic key

A single declared Struct can select a field using a String key supplied by a
callback. These examples try the `asset` and `elapsed` fields with both
`get(key)` and dynamic bracket access:

```hff
["asset","elapsed"]:any(key=>labels:get(key):type="bytes")
["asset","elapsed"]:map(key=>labels[key]:type):json=["bytes","duration"]:json
```

Both match rows 1 and 2. The first query finds the Bytes `asset` field; the
second observes the declared Bytes and Duration types in order. Rows 3 and 4
do not match because `asset` is null. The key is evaluated once, and the
selected field keeps its own declared type. This applies to a schema-known
single or nullable Struct and explicit objects, not to unresolved Struct
unions or same-position ambiguous alternatives.

The selected field's declared type can also flow into a callback consumer or
a nested `flatmap()` predicate:

```hff
["asset","elapsed"]:map(key=>labels:get(key)):count(value=>value:type="bytes")=1
["asset","elapsed"]:any(key=>[labels:get(key)]:flatmap(value=>[value]):any(value=>value:type="bytes"))
```

Both match rows 1 and 2, where `asset` is non-null Bytes and `elapsed` is
Duration. Rows 3 and 4 do not match because `asset` is null and `elapsed` is
not Bytes. The declared field schema supplies Bytes identity; a string in
native JSON `Any` does not acquire that identity from its spelling. A
no-value produced by an earlier map guard is not erased by a later filter.

### Enumerate heterogeneous Struct values

`values()` keeps each present field's declared type while enumerating a
schema-known heterogeneous Struct or explicit object. With the `events`
fixture, `app`, `asset`, `elapsed`, and `recorded` are enumerated in `C`
collation order:

```hff
labels:values:any(value=>value:type="bytes")
labels:values:map(value=>value:type):json=["string","bytes","duration","timestamp"]:json
```

Both queries match rows 1 and 2. The first finds the non-null Bytes value; the
second checks the ordered field types. Row 3 retains its explicit null `asset`
value, so its type list contains `"null"`; row 4 retains four null values.
Missing fields would be omitted. A string in the `Any` `app` field remains a
String even when its text resembles base64 or a Timestamp. This does not infer
tagged identity from native JSON `Any` strings or resolve ambiguous stored
unions.

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
consumer. Folds can also preserve recovered values in constructed typed trees;
the aggregate section below shows the bounded numeric case. Other materialized
values still need recoverable type information.

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

### Fold over deferred item values

Fold callbacks can retain recovered `items()` values inside constructed
accumulators, then inspect or serialize them later. With `labels` and the four
records above, the first query grows a list of mixed tagged values and observes
their types after the fold. The second keeps the latest value in an object
field:

```hff
labels:items:map(pair=>pair[1]):foldl([], (values,value)=>values:concat([value])):map(value=>value:type):json=["string","bytes","duration","timestamp"]:json
labels:items:map(pair=>pair[1]):foldl({}, (acc,value)=>{"previous":acc,"value":value}).value:type="timestamp"
```

The list query matches rows 1 and 2. The object query matches rows 1–3; row 4's
last value is null. The PostgreSQL backend keeps the source schema with these
constructed accumulator values, so later maps and field access can recover
Bytes, Duration, and Timestamp instead of treating every stored string alike.
A null value is passed to the callback rather than skipped. An empty list
returns its seed, and a null receiver returns null before the seed or callback
is evaluated. Guard-produced no-value propagates through the whole fold.

### Enumerate a folded object's fields

The same typed values can be retained in an object and enumerated afterward.
Using `typed_events` and its four rows above, these queries keep the `asset`
Bytes value in a field named `v`:

```hff
labels:items:filter(pair=>pair[0]="asset"):foldl({"v":null},(acc,pair)=>{"v":pair[1]}):values:map(value=>value:type):json=["bytes"]:json
labels:items:filter(pair=>pair[0]="asset"):foldr({"v":null},(pair,acc)=>{"v":pair[1]}):items:map(pair=>pair[1]:type):json=["bytes"]:json
labels:items:filter(pair=>pair[0]="asset"):foldl({},(acc,pair)=>{"v":pair[1]}):keys:json=["v"]:json
```

All three queries match rows 1 and 2. Rows 3 and 4 have non-object roots, so the
`items()` receiver returns null and the fold and later enumeration return null.
The private fold result retains the Bytes type for `values()` and
`items()`; `keys()` only observes the fixed field name. This does not infer a
tagged type from an ambiguous stored JSON string.

Typed-tree `in()`, list `contains()`, `unique()`, and `distinct()` can use the
same recovered value identities as these comparisons. Bounded sorting of
recovered typed trees is shown in
[Sort recovered deferred values](#sort-recovered-deferred-values). Aggregates
on folded typed lists are supported when their numeric element types can be
recovered, as shown below. Other consumers may still need an explicit guard or
fail `Build` when a value's type cannot be recovered. Native JSON strings do
not imply tagged identity, and JSONB still needs numeric metadata when storage
erased a distinction the operation depends on.

### Compare a folded typed list

A folded typed tree can be compared directly with another tree from the same
source, without serializing either list first:

```hff
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value]))=labels:items:map(pair=>pair[1])
```

All four `events` rows match. The lists preserve object-key order and the same
recovered value types; the stored null members in rows 3 and 4 compare equal
inside the lists. This is structural equality, so different element families
are unequal and mixed Int/Float values compare by exact numeric value. A
top-level dynamic type mismatch yields null, while comparisons involving
top-level null or no-value are false except for an explicit `=null` test.
Bounded sorting of recovered typed trees is shown in
[Sort recovered deferred values](#sort-recovered-deferred-values).
The aggregate section below shows the supported numeric consumers. These
comparisons do not make every operation valid for every ambiguous tree.

### Membership and distinct on folded typed values

Membership and deduplication can also consume a fold result while it retains
its per-field types:

```hff
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value])):contains("YQ==","YQ==":frombase64)
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value])):concat(labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value]))):distinct:size=4
```

The membership query matches row 1, where `app` is the String `"YQ=="` and
`asset` is the Bytes value represented by that base64 text. The second query
matches rows 1–3: each has four distinct field values, with row 3's null
counting as one value. Row 4 has four nulls, which `distinct()` reduces to one.
These examples compare scalar values; structural list/object elements also
require the applicable equality capabilities. See the PostgreSQL JSON guide's
[null and no-value behavior for `contains()`](../backends/postgresql/json.md#contains-on-dynamic-strings-and-lists)
for the difference between String and List receivers.

### Sort recovered deferred values

`sort()` can consume a deferred list when its values retain an orderable type.
These queries fold each record's values into a typed list. The first retains
String values, appends an empty String, and sorts with PostgreSQL's `C`
collation; the second sorts the mixed list directly:

```hff
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value])):filter(value=>value:[string]:exists):concat([""]):sort:json="[\"\",\"YQ==\"]"
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value])):sort:type="null"
```

The first query matches row 1: its only String value is the `app` value
`YQ==`, and the appended empty String sorts before it. Row 2's `app` value is
the Timestamp-looking String, which remains a String but does not match the
expected list. The second query matches rows 1–4 because each folded list
contains mixed value families or null and therefore has no single runtime sort
domain. An empty list still sorts to an empty list. Deferred sorting preserves
the original values and stable order; it does not enable unrelated consumers
of every ambiguous tree.

### Find the first typed deferred value

`find()` can return a recovered value from a folded list. These queries select
the first Bytes value, then the first Timestamp value, in each record's
object-key order:

```hff
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value])):find(value=>value:[bytes]:exists):string="YQ=="
labels:items:map(pair=>pair[1]):foldl([], (acc,value)=>acc:concat([value])):find(value=>value:[timestamp]:exists):type="timestamp"
```

The first matches row 1, whose Bytes value is `YQ==`; row 2's first Bytes
value is `Yg==`. The second matches rows 1–3, whose first Timestamp value is
the `recorded` field. Row 4 has no matching non-null value. A guarded
no-value predicate is false, and SQL preserves the first-match result without
promising Local's physical callback short-circuit.

### Aggregate a folded typed list

These queries fold each key together with its original, heterogeneous value.
The aggregate callback reads only the String key and projects its length as
Int. The four keys `app`, `asset`, `elapsed`, and `recorded` have lengths 3, 5,
7, and 8, so all four records match each query:

```hff
labels:items:map(pair=>{"key":pair[0],"value":pair[1]}):foldl([], (acc,item)=>acc:concat([item])):sum(item=>item.key:len)=23
labels:items:map(pair=>{"key":pair[0],"value":pair[1]}):foldl([], (acc,item)=>acc:concat([item])):avg(item=>item.key:len)=5.75
labels:items:map(pair=>{"key":pair[0],"value":pair[1]}):foldl([], (acc,item)=>acc:concat([item])):min(item=>item.key:len)=3
labels:items:map(pair=>{"key":pair[0],"value":pair[1]}):foldl([], (acc,item)=>acc:concat([item])):max(item=>item.key:len)=8
```

The folded list retains each Bytes, Duration, Timestamp, String, or null value;
only the numeric key-length projection contributes to the aggregate. For
typed trees with Float contributions, PostgreSQL uses the backend Float
aggregate profile described in the
[JSON backend guide](../backends/postgresql/json.md#numeric-list-extrema):
numeric intermediate accumulation and a checked final Float conversion. Null
and guarded no-value contributions are skipped; a reached nonnumeric
contribution makes the result null. A no-value result for the whole source
remains no-value. JSONB still requires numeric metadata when storage erased a
numeric subtype needed by the aggregate.

### Fold pair and pair-list accumulators

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
