# List and Object Methods

The local backend implements the list and object method families for immutable
HFF values. The complete signatures and language rules are in the [method
catalog](../reference/methods.md); this guide focuses on useful query patterns.

## List queries

`count()` returns the number of elements. Its predicate form counts elements
whose callback result converts to true; `filter()` returns the original
matching elements in their existing order:

```hff
scores:sum=12
scores:sum(score=>score!)=12
items:count
items:count(item=>item.active)=2
items:filter(item=>item.active):size>=1
```

For a schema-known `List<Int|null>` value `[5, null, 7]`, both sum queries
return 12. The second query demonstrates that a guarded no-value projection is
skipped just like a null value.

Both predicate methods treat a guarded no-value callback result as false for
that element. A null receiver yields null. `filter()` does not map or otherwise
change retained values; filtering an empty list returns an empty list. The
zero-argument `count()` requires `method:list.count`; the callback form and
`filter()` also require the `Callbacks` capability. Nested callbacks require
`NestedCallbacks` as well. A selected backend can still reject a query it
cannot lower faithfully.

`sum()` accepts a numeric list or a callback that projects each element to a
number. Null values and guarded no-value projections are skipped. An empty list
or a list with no contributing values returns Int zero. A runtime nonnumeric
contribution returns null; all-Int contributions are exact and overflow to
null if their final total is outside int64. If any contribution is Float, the
result is Float and follows the backend's declared Float model; a non-finite
result is null. The zero-argument form requires `method:list.sum`; the callback
form requires that method capability and `Callbacks`.

The PostgreSQL JSON/JSONB backend supports no-argument and callback sums over
recoverable mixed numeric lists. For a `List<Int|Float|null>` binding,
`scores:sum` returns Float for `[1, 2.5]`. A `List<Any>` binding can sum the
same values when their runtime numeric subtypes are recoverable. Callback
projections can also produce mixed or dynamic numeric contributions when those
subtypes remain recoverable. For example, with `scores` bound as
`List<Int|Float|null>`, `values` as `List<Any>`, and `items` as
`List<Struct{price:Int|Float|null}>`:

```hff
scores:sum=3.5
scores:sum(item=>item)=3.5
values:sum=3.5
items:sum(item=>item.price)=3.5
```

Null contributions are skipped; a nonnumeric runtime value in `values` makes
the result null. An empty or all-skipped list returns Int zero. If every
contribution is Int, the final sum is exact when it fits int64. If any
contribution is Float, the result is Float. For JSONB, provide version-1
numeric type metadata when stored numbers may have lost their Int/Float
distinction. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#schema-known-collection-access)
for the representation limits. PostgreSQL obtains round-trip-safe
Float text for each contribution, accumulates those decimal values with
`SUM(numeric)`, and checks the final Float conversion. This requires
`extra_float_digits > 0`; HFF does not set the database session value. Numeric
accumulation can differ from Local's list-order Float additions.

```hff
tags:size>=2
tags:has(-1)
tags:contains("api", "production")
tags:unique
tags:sort=["api", "production"]
tags:concat(["internal"]):contains("internal")
```

`size()` returns the number of elements. `has(index)` accepts integer-valued
numbers and supports negative indexes from the end; an out-of-range index is
false and a non-integer index yields null. `contains()` requires one or more
arguments and is true only when every argument occurs in the receiver. Both
`contains()` and `unique()` use structural HFF value equality, so lists and
objects compare by contents and numeric subtypes compare by numeric value.

`sort()` returns a stable sorted copy and does not modify its input. Elements
must share an orderable type; mixed integer and float values are allowed. A
runtime list with incompatible or non-orderable members produces null.
The PostgreSQL backend also sorts `Any` and list-union JSON values when their
stored element representations are recoverable; JSONB may need numeric type
metadata. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#sort-json-backed-lists)
for the supported subset.
`concat(list)` returns a new list containing the receiver followed by the
argument.

## Object queries

```hff
labels:size=2
labels:has("team")
labels:get("team")=null
labels:keys=["app", "team"]
labels:values=["auth", null]
labels:items=[["app", "auth"], ["team", null]]
```

`has(key)` checks whether the key exists, including when its value is null.
`get(key)` returns null for a missing key, so use `has()` when the distinction
between a missing key and a stored null matters. `keys()`, `values()`, and
`items()` enumerate keys in Unicode code-point order. Each item is a
two-element `[key, value]` list, and the values and items use the same order as
the keys.

## PostgreSQL collection filters

The PostgreSQL backend can answer some root collection filters without
decoding or aggregating the complete value:

```hff
tags=[]
tags:size=0
tags:size>0
labels={}
items=[]
items
items:size=0
```

For an unguarded, statically typed root JSONB binding without type metadata,
empty list/object comparisons and bare collection truth use the physical
JSONB value. Nonzero literal `size()` comparisons use a guarded
`jsonb_array_length` for a List. SQL NULL and JSON null remain false as filter
receivers; nonzero Map/Struct sizes retain general lowering. A plain relation
can use a correlated existence check or a state/constant test for empty list
comparisons, truthiness, and zero-cardinality comparisons; higher cardinality
thresholds retain the count-based lowering. Nested JSON paths,
metadata-backed bindings, guards, computed receivers, and dynamic values use
general lowering. See the [PostgreSQL backend guide](../backends/postgresql/README.md)
for binding details and the [translation optimization notes](../backends/postgresql/optimizations.md#lower-direct-collection-emptiness-and-cardinality-filters)
for the exact scope.

## Dynamic values and capabilities

The compiler checks capabilities for every method family that can apply to a
static union. At runtime, the receiver's actual family selects the method
implementation; an incompatible dynamic receiver yields null. An explicit type
guard can narrow a call to one family:

```hff
value:[list[string]]:contains("api")
```

The guard preserves a matching list and produces internal no-value for a
different runtime type. This is useful with an `Any` field or union. Capability
selection happens during compilation; local backend support is checked by
`local.Build`.

Collection results are newly allocated and the input values remain immutable.
Local per-match `MaxSteps` and `MaxGeneratedValues` limits cover traversal and
generated collection containers and entries. Cancellation or a limit error
aborts evaluation without returning a partial match. See the [local evaluation
guide](../backends/local/README.md) for backend configuration and error handling.

Run the complete example from the repository root:

```sh
go run ./examples/collections
```

It prints:

```text
tags:contains("api","production") labels:has("team") -> true
tags:sort=["api","production"] -> true
labels:keys=["app","team"] -> true
```
