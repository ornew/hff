# List Callbacks and Transformations

List callbacks let a query inspect or transform each element without adding a
general-purpose function language. Callback parameters are inferred from the
list and are scoped to the callback expression. The full rules are in the
[method catalog](../../spec/methods.md) and [type specification](../../spec/types.md).

## Select and check elements

```hff
items:any(item=>item.price>100)
items:all(item=>item.available)
orders:any(order=>order.active order.items:any(item=>item.price>10))
items:any(item=>item.active | item.price>10)
```

`any()` is true when at least one callback result converts to true;
`all()` is true when every result converts to true. Both visit from left to
right and stop once the answer is known. An empty list returns false for
`any()` and true for `all()`. A null receiver returns null. A guarded no-value
callback result converts to false for that element; cancellation and limit
errors remain errors.

## Transform and fold

```hff
items:map(item=>item.name)=["Book","Pen"]
items:flatmap(item=>[item.name])=["Book","Pen"]
items:foldl(0,(total,item)=>total+item.price)>100
items:foldr("",(item,tail)=>item.name:concat(tail))="BookPen"
```

`map()` visits left to right and preserves ordinary null results as list
elements. `flatmap()` also visits left to right and appends each returned list;
a callback result that is dynamically not a list makes the whole result null.
Guarded no-value from either transformation propagates to the whole call.

`foldl(initial,(acc,item)=>expr)` visits left to right, while
`foldr(initial,(item,acc)=>expr)` visits right to left. Both return the initial
accumulator for an empty list. The accumulator type includes the initial value
and possible callback results; a null result becomes the next accumulator, and
guarded no-value propagates to the entire fold.

Callback parameters use lexical scope and may capture unshadowed outer schema
variables and outer callback parameters. Parameter names may shadow an outer
name. Nested callbacks keep their own parameter bindings. AND and OR retain
left-to-right short-circuit behavior inside callbacks; in the nested example,
the inner list is examined only for active orders. Nested callbacks require the
`NestedCallbacks` capability, which depends on `Callbacks`. `MaxCallbackDepth`
defaults to 64, counting the outermost callback as depth 1. Callback expressions
cannot call general user-defined functions or perform side effects.

## Run the example

The example uses a list of products to select, check, map, and fold:

```sh
go run ./examples/callbacks
```

Expected output:

```text
items:any(item=>item.price>100) -> true
items:all(item=>item.available) -> false
items:map(item=>item.name)=["Book","Pen"] -> true
items:foldl(0,(total,item)=>total+item.price)>100 -> true
```

Local evaluation applies the per-match step and generated-value budgets and
checks context cancellation while traversing callbacks. An error aborts the
match without returning a partial result; see the
[local evaluation guide](local-evaluation.md) for configuration.
