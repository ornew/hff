# 006: Method Calls Without Empty Parentheses

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

Allow a method call with no supplied arguments to omit its parentheses:

```hff
name:len
name:len()
```

These spellings make the same call. This also applies to methods whose
parameters are all optional: `created_at:year` and `created_at:year()` use the
same default. A method with required parameters still fails compilation when
they are omitted.

## Motivation

An empty argument list adds no information to a zero-argument call. Omitting it
makes common expressions shorter while preserving explicit parentheses for
readers who prefer them. Optional-argument methods already define behavior when
called with zero supplied arguments, so the shorthand should work for them as
well.

## Goals

- Make parentheses optional only when the supplied argument count is zero.
- Preserve existing explicit-call syntax and method resolution.
- Apply the same rule to methods with no parameters and methods whose parameters
  are all optional.
- Keep missing required arguments as compile-time errors.

## Non-goals

This does not add property-style access, free-function calls, implicit method
arguments, or a way to omit only some positional arguments.

## Design

The [syntax specification](../../spec/syntax.md#method-calls) treats a bare
method name after a receiver colon as a zero-argument method call. The compiler
then applies the existing signature rules: no-argument methods resolve
normally, optional parameters use their existing defaults, and required
parameters produce the existing argument-count diagnostic. Explicit empty
parentheses remain valid and equivalent.

Because this is syntax sugar, it does not change method IDs, capability
selection, result types, null/no-value behavior, source ownership, or backend
support. Generated syntax nodes should represent both forms consistently while
retaining source spans for the spelling the user wrote.

## Alternatives considered

Restricting the shorthand to signatures declared with zero parameters would
keep optional-parameter calls visually distinct. It would also make syntax
depend on the method catalog before users could understand whether parentheses
were required. Treating every no-argument spelling uniformly is simpler; the
existing compiler already distinguishes optional from required arguments.

## Testing

Parser and compiler conformance should cover explicit and omitted parentheses
for zero-argument calls, optional-argument defaults, omitted required
arguments, chaining, and source spans. Runtime parity should confirm both
spellings produce identical behavior for local and future backends.
