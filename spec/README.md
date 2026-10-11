# HFF Language Specification

HFF (Human-Friendly Filter) is a pure, type-safe filter language for queries
written by people in service interfaces. It has a fixed syntax, configurable
semantic capabilities, a portable runtime, and a design that allows translation
to arbitrary processing systems.

## Contents

| Chapter | Defines |
|:--|:--|
| [Syntax](syntax.md) | Names, values, operators, precedence, access, method calls, and typed PEGO parsing |
| [Types and schemas](types.md) | Value types, declared variables, structs, maps, unions, explicit type guards, and dynamic dispatch |
| [Evaluation](evaluation.md) | Boolean conversion, null propagation, comparisons, value and type guards, and filter restrictions |
| [Methods](methods.md) | Configurable method signatures, durations, and time zones |
| [Capabilities and backends](capabilities.md) | Compilation, feature selection, purity, portability, and backend equivalence |

## Scope and priorities

The primary unit is a filter evaluated against data supplied by a service.
HFF favors human intuition over unrestricted control, service-specific feature
sets over the ability to express every computation, and continued evaluation
over requiring users to handle missing-data errors.

This does not eliminate syntax errors or compile-time validation. A malformed,
ill-typed, unsupported, or non-filter query is rejected before evaluation.
Null handling is part of the language's evaluation semantics, rather than a
license for a compiler to ignore errors.

HFF does not expose general-purpose statements, assignment, or arbitrary
side-effecting programs. Higher-order list methods use named arrow callbacks
with inferred parameters and pure, finite collection traversal.

## Conventions

`hff` code blocks contain HFF source. `text` blocks may contain pseudocode,
schemas, input records, or expected results. Schema notation in these documents
is explanatory and does not define a schema declaration language.

MUST and MUST NOT express requirements. SHOULD expresses a recommendation.
MAY expresses an allowed implementation choice. Proposals are labeled explicitly.
Examples containing literal receivers illustrate subexpression semantics; they
do not override the requirement that a complete filter relate to service data.

The grammar is written in PEGO and generates an internal typed Go parser.
The document structure and parser-generation approach are informed by the local
PEGO [specification](https://github.com/ornew/pego/blob/main/spec/README.md) and
[code generation guide](https://github.com/ornew/pego/blob/main/docs/guide/code-generation.md).
These are development references, not dependencies of the HFF language contract.

## Implementation status

The public compiler and a local evaluator for core operators, literals,
indexing, guards, short-circuit logic, scalar/string methods, list/object
methods, and collection callbacks are implemented. Base64 conversion methods
are also available locally. Additional temporal/JSON methods, SQL translation,
and extension registration are not yet available in the local backend. A backend
rejects unsupported compiled operations explicitly; compile validity alone does
not promise support from every backend.
