# Design Records

These documents explain the rationale, contracts, and tradeoffs behind HFF's
language design. The [language specification](../../spec/README.md) defines the
query language; records provide context for individual design choices.

| Record | Subject |
|:--|:--|
| [001: Compact predicates](001-compact-predicates.md) | Whitespace as a visual and logical boundary |
| [002: Guards and presence](002-guards-and-presence.md) | Value-preserving guards and explicit existence tests |
| [003: Collection predicates](003-collection-predicates.md) | Short-circuiting any/all for common search intent |
| [004: Go API](004-go-api.md) | Configuration, compilation, and backend construction |
| [005: Explicit type guards](005-explicit-type-guards.md) | Narrow dynamic values without conversion and keep capability checks static |
| [006: Method calls without empty parentheses](006-method-calls-without-empty-parentheses.md) | Omit parentheses when a method call supplies no arguments |
| [007: Structural equality capabilities](007-structural-equality-capabilities.md) | Select list and object content equality independently |
| [008: Versioned time-zone profiles](008-versioned-time-zone-profiles.md) | Resolve named zones from caller-supplied TZif data |

Use [000: Design Record Template](000-template.md) for a new `NNN-topic.md`
record. Proposed, Accepted, and Implemented distinguish a proposal, an approved
design, and delivered behavior.
