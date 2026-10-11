# 003: Collection Predicates

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

List any/all methods express existential and universal search without a reduction
or an intermediate transformed list. Callback expressions can nest and can use
the ordinary short-circuiting AND and OR operators.

```hff
items:any(x=>x.price>10)
items:all(x=>x.active)
orders:any(order=>order.active order.items:any(item=>item.price>10))
items:any(item=>item.active | item.price>10)
```

## Motivation

Finding any matching item and checking every item are common filtering tasks.
Encoding these operations through fold asks the reader to understand an
accumulator algorithm. Direct predicate methods expose the search intent.

## Design

Both methods evaluate elements left to right and boolean-convert callback
results. Any stops at the first true result; all stops at the first false result.
Empty lists yield false for any and true for all. Null receivers yield null.
A callback's guarded no-value becomes false for that element. Cancellation
remains an explicit aborted result, never a false match. Callbacks use inferred
parameter types and lexical scope. An inner callback may capture unshadowed
outer callback parameters and schema variables; a nested parameter may shadow
an outer name. See [methods](../../spec/methods.md).

Nested callbacks use the same arrow syntax as top-level callbacks. Logical AND
and OR work in callback bodies and retain HFF's normal left-to-right
short-circuit behavior. In the nested example, an order's items are inspected
only when that order is active; the inner any stops at its first matching item.
The second example succeeds when either condition is true for an item.

Transformation methods such as map preserve null elements and propagate guarded
no-value to the whole call. Predicate methods instead answer a matching question,
so they consume the callback result through ordinary boolean conversion.

## Alternatives considered

Fold can express these results but adds accumulator syntax and requires a
separate short-circuiting mechanism. Mapping predicates first creates an
unnecessary list. Propagating one callback's no-value through the whole predicate
call prevents a later valid element from satisfying any. Element-level boolean
conversion keeps search behavior aligned with ordinary filtering.

## Compatibility and backend support

Nesting reuses the existing callback and method-call syntax. The `Callbacks`
capability enables callback methods; nested callback expressions additionally
require `NestedCallbacks`, which depends on `Callbacks`. `MaxCallbackDepth`
defaults to 64. The outermost callback has depth 1, and each callback nested
inside its body adds one. Queries beyond the configured depth are rejected
before evaluation.

Compilation uses shared `MaxCompileSteps` and `MaxCallbackDepth` limits.
`MaxCompileSteps` bounds semantic analysis work, including repeated callback
inference visits; `MaxCallbackDepth` bounds nested callback structure. The
outermost callback has depth 1.

The local runtime charges callback expression visits to the same per-match
`MaxSteps` budget. Re-entering a callback body for another element incurs its
work again; short-circuited branches and callbacks are not evaluated. Collection
results use the existing `MaxGeneratedValues` budget. These limits bound local
work without assigning a separate step budget to each callback.

A SQL backend reports the execution and resource constraints it can enforce.
It cannot promise the same per-match `MaxSteps` behavior merely by translating
a query: the database controls planning and may reorder or skip expressions.
Callers configure database timeouts through their database integration. If a
caller requests an execution constraint the SQL backend cannot enforce, build
must reject that request rather than silently ignore it. Shared compilation
limits still apply before SQL generation. A future backend must also preserve
empty-list, null, guard, capture, nesting, and short-circuit semantics.

## Testing

Conformance should cover empty lists, nullable elements, captured variables,
truthiness conversion, nested callbacks, AND/OR in callback bodies, required
capability dependencies, compile-step and callback-depth boundaries, early
matches, early failures, and cancellation. A local runtime should verify that skipped
callbacks consume no evaluation steps and repeated nested visits count toward
the shared per-match budget. SQL tests should verify reported enforceable
constraints and rejection of requested unsupported constraints.
