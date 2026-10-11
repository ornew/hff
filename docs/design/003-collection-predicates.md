# 003: Collection Predicates

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

List any/all methods express existential and universal search without a reduction
or an intermediate transformed list.

```hff
items:any(x=>x.price>10)
items:all(x=>x.active)
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
parameter types and lexical scope. See [methods](../../spec/methods.md).

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

The fixed call syntax requires no grammar extension. Capabilities can disable
these methods, and backends must preserve their empty-list, null, and guard
semantics. Execution-order guarantees depend on the backend's advertised
constraints rather than an assumed SQL evaluation order.

## Testing

Conformance should cover empty lists, nullable elements, captured variables,
truthiness conversion, early matches, early failures, and cancellation. A local
runtime should verify that skipped callbacks consume no evaluation steps.
