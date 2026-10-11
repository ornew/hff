# 002: Guards and Presence

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

Postfix `!` guards a value and preserves it on success. `exists()` explicitly
checks presence, independently of truthiness.

```hff
parent!.value:exists
age!+1>10
```

## Motivation

Null-safe access simplifies filters but cannot identify which part of a path was
null. A guarded path must distinguish an unreachable field from a field that was
reached and contains null. Presence is also different from truthiness: false,
zero, and empty collections can all be present values.

## Design

A failed guard produces an internal no-value state. It propagates through value
operations; comparisons and boolean conversion interpret it as false. Successful
guards return the original value in every context. Consequently `age!` remains
falsy for zero. `exists()` returns true for any present non-null value, and false
for null and no-value. It is an explicit consumer of the internal state.

```hff
parent!.value=null
parent!.value:exists
```

The first can match a null field only if the parent exists; the second requires
both a parent and a non-null field. Prefix NOT negates the resulting predicate
normally. See [evaluation](../../spec/evaluation.md) for propagation and null
comparison rules.

## Alternatives considered

Replacing guard failure with ordinary null would allow an unreachable field to
match `=null`. Interpreting a final guard as an existence boolean is concise,
but makes moving the expression into a value context change its meaning.
Keeping guards value-preserving and providing an explicit method separates the
two operations and supports arithmetic and argument composition.

## Compatibility and backend support

An implementation must retain no-value separately from stored null, or encode
an equivalent guarded condition. A backend that cannot preserve these results
must reject the relevant operation. The internal state cannot be supplied by a
host or serialized as ordinary data.

## Testing

Conformance should cover null parents, present null fields, zero/false/empty
values, negation, OR alternatives, collection propagation, and exists after a
failed path guard. These are validation requirements for implementations.
