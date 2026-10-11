# 001: Compact Predicates

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

Arithmetic and comparison operators are adjacent to their operands. Whitespace
joins predicates with AND, making each compact expression a visual condition.

```hff
age>10 name="Alice"
(age>=10 | age<=5) name="Alice"
```

## Motivation

A filter is read as well as written. Spaces inside a condition and spaces between
conditions would serve competing purposes. Keeping one condition compact exposes
its boundaries without requiring a reader to reconstruct a full expression tree.

## Goals

Preserve readable condition units, implicit AND, and one grammar across services.
Keep logical grouping explicit and independent of capability selection.

## Design

The [syntax specification](../../spec/syntax.md) defines compact arithmetic and
comparison operators. `age > 10` is invalid; `age>10` is valid. Spaces, tabs, and
line breaks between predicates mean AND. Padding around OR, grouping, collection
separators, and callback arrows is allowed. Strings and quoted names retain their
own whitespace. Access and method punctuation are compact.

`a-b` is subtraction. `a -b` joins two predicates, the second of which is a
negative value. OR binds less tightly than AND. Parsing retains predicate
whitespace rather than discarding it through a blanket whitespace skip.

## Alternatives considered

Permitting operator padding accepts familiar typing styles but makes whitespace
both an internal formatting choice and a predicate separator. Prioritizing binary
operators would also change the reading of `a -b`. Explicit AND symbols remove
that ambiguity but increase punctuation in the common sequence of conditions.
Compact operators preserve the intended visual structure.

## Testing

Parser conformance should distinguish operator padding from permitted separator
padding, preserve whitespace in strings and quoted names, and cover every
precedence level. These are language requirements, not completed parser tests.
