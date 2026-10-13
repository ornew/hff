# 019. PostgreSQL JSON Union Dispatch

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-13

## Summary

The PostgreSQL JSON and JSONB translators support a schema union when
the stored representation and configured metadata let the query recover the
active HFF alternative wherever an operation needs it. A union such as
`Duration|Int` has disjoint JSON categories (string and number) and can be
dispatched from the declared schema plus the stored category. A union such as
`String|Duration` cannot: both alternatives are JSON strings. Build must reject
an operation that would have to guess an unrecoverable HFF identity.

This record defines the recovery rule for dynamic operations over JSON-backed
unions: when schema and stored representation provide enough
evidence to dispatch faithfully, and when translation must reject an
expression rather than guess.

## Motivation

JSON-backed schema fields can contain values whose HFF type is selected at
runtime. A declared union gives the translator a finite set of alternatives,
but it does not always identify which alternative a stored value represents.
Silently choosing one can change `type()`, guards, comparisons, method
dispatch, or serialized output.

Ordinary JSON categories provide some type evidence. For example, a JSON string
and a JSON number distinguish `Duration|Int`, while two HFF types represented
as strings are indistinguishable without additional information. JSONB also
normalizes numeric values, so a number alone may not distinguish `Int` from
`Float`. The existing JSONB metadata contract addresses numeric subtype and
value restoration only; it is not a general tagged-union encoding.

## Goals

- Dispatch only among alternatives admitted by the declared schema.
- Recover an alternative from JSON category when its representation is
  disjoint from the other alternatives.
- Use configured metadata only for distinctions that the metadata format
  represents and JSONB has erased.
- Preserve HFF behavior for type inspection, truthiness, guards, comparisons,
  membership, collection operations, callbacks, and serialization.
- Reject plans at Build when an operation requires an identity that cannot be
  recovered from schema, stored representation, and supported metadata.
- Preserve the distinction between JSON null, a missing object member, and
  HFF no-value.

## Non-goals

- Adding a custom tagged representation to application JSON or JSONB columns.
- Extending JSONB metadata version 1 with Bytes, Duration, or arbitrary union
  tags in this record.
- Inferring or validating stored values by querying the database during Build.
- Promising support for every operation merely because a union's alternatives
  have different JSON categories.

## Design

### Public behavior and contracts

The declared schema defines the alternatives a stored value may have. At a
dynamic operation, the translator may select an alternative only when the
runtime representation supplies enough evidence to do so. JSON `null` remains
null according to the existing HFF contract; it is not treated as an
alternative tag. Missing members and no-value continue to follow their
existing presence and propagation rules.

For category-disjoint unions such as `Duration|Int`, `Duration|Bool|Null`,
`Duration|Int|Bool|Null`, `Duration|Float|Null`, `Timestamp|Bool|Null`, and
`Timestamp|Float|Null`, a JSON string selects the string-backed schema type, a
number selects the declared numeric alternative, a boolean selects Bool when
present, and JSON null remains null. The same rule applies in JSONB when the
declared alternatives make the active branch recoverable. The
`Duration|Float|Null` subset supports type inspection, type guards,
comparisons, and membership without numeric metadata. Since `type()` reports
both Int and Float as `number`, use a `[float]` guard to distinguish Float.
Native JSON preserves Float signed zero for serialization. Build rejects JSONB
Float-union serialization when storage may have erased signed-zero provenance.
For `Int|Float`, JSONB numeric
metadata is required wherever the operation observes which numeric subtype is
present or needs representation details that JSONB discarded. Native JSON may
retain the numeric spelling needed by the active profile, subject to that
profile's existing rules.

The `Timestamp|Int|Float|Null` and `Duration|Int|Float|Null` subsets are also
supported with native JSON spelling or JSONB version-1 numeric metadata.
Supported operations include type inspection, explicit guards, comparisons,
membership, list `contains()`/`unique()`, and `string()`/`json()` when the
active value remains recoverable. JSONB requires the sidecar for operations
that observe Int versus Float; without it, Build rejects those operations.
Metadata `text` restores Float representations such as negative zero when
needed. A Float-only operand bound as JSONB can be compared with a mixed-union
operand bound as native JSON, because the known Float schema fixes its type.
This does not make every collection or serialization operation available. Raw
JSONB mixed-numeric unions do not have a metadata-free lowering for operations
that require the numeric subtype.

For schema-known list element unions containing one temporal alternative and
numeric alternatives, `sort()` dispatches from each stored element's JSON
category. A list containing only temporal strings sorts by exact Timestamp or
Duration value; one containing only numbers sorts numerically across Int and
Float. Equal numeric values retain input order and representation. A mixed
temporal/numeric list, null element, or other non-orderable element produces
ordinary null. Mixed Int/Float JSONB sorting requires metadata when the
operation needs the erased subtype; raw JSONB remains a Build error in that
case. This operation-specific support does not imply arbitrary callback
support for the union.

Support remains operation-specific: category-disjointness alone does not
make every result representable. Metadata-free JSONB does not reveal whether
a stored number is Int or Float. Serialization of a JSONB Float branch also
requires lossless signed-zero provenance.

For a representation-ambiguous union such as `String|Duration` or
`Bytes|Duration`, ordinary JSON string storage does not identify the active
alternative. The translator must reject an operation that needs that identity
unless a separately accepted metadata contract supplies it. JSONB metadata
version 1 currently describes numeric types only; this design does not
reinterpret its `int` or `float` markers as string, Bytes, or Duration tags.

### Operation semantics

- `type()` follows the HFF contract for the recovered alternative. In
  particular, both Int and Float report `"number"`; an explicit `[float]` or
  `[int]` guard is needed to distinguish those subtypes. It must not report a
  type inferred solely from a representation shared by alternatives.
- Truthiness and explicit type guards dispatch using the recovered
  alternative. A guard may narrow the alternatives only when the underlying
  representation can establish the test faithfully.
- Comparisons and membership use the local HFF rules for the recovered
  operand types, including ordinary-null results for incompatible runtime
  families and the existing distinction between top-level incompatibility
  and nested structural mismatch. A translator must reject a plan when it
  cannot choose the rule required by the active alternative.
- Methods and collection operations dispatch only for signatures supported by
  the recovered alternative. A category-disjoint schema does not by itself
  guarantee that every method or collection shape can be lowered.
- Callback inputs, callback results, accumulators, and constructed collections
  retain their active HFF type while later expressions or serialization can
  observe it. Lowering must not normalize a union to one arbitrary schema
  member.
- `string()` and `json()` serialization preserve the recovered HFF value and
  its required representation details. If the SQL representation or available
  metadata cannot reproduce them, Build rejects the expression.

### Metadata and storage format

This design uses the existing JSON and JSONB binding contracts. JSONB may use
the separate version-1 JSON Pointer metadata object, for example
`{"version":1,"types":{"/price":{"type":"float","text":"-0.0"}}}`.
The `types` map currently restores numeric HFF types (`int` and `float`) and
optional numeric text when JSONB has lost a needed distinction. Pointer
escaping, array-index segments, and the empty-key representation remain as
defined by the JSONB metadata record.

The application stores the JSONB document and metadata consistently. The
caller guarantees that metadata version, pointers, markers, text, and stored
values agree with the declared schema. Build performs no database I/O and
does not validate stored rows or the complete metadata document. Missing or
invalid binding configuration, or an expression whose required type cannot be
recovered under that contract, is a Build error.

No new metadata tag is defined here for Bytes, Duration, or arbitrary union
identities. If future support requires such tags, it needs a separate design
for versioning, pointer-level validation responsibilities, and compatibility
with existing application writers.

### Implementation and invariants

The translator carries the set of schema alternatives alongside the
representation of a JSON-backed value. At each operation it derives a
dispatch only from reliable evidence: declared schema, JSON category, and
supported metadata. A dispatch must remain scoped to the value and binding
that produced that evidence; callback parameters, captured values, and
shadowed names must not inherit another value's type evidence.

Presence state remains independent of union identity. JSON null, missing
object members, and no-value cannot be collapsed to make dispatch easier.
Likewise, numeric metadata restoration must happen before an operation whose
local semantics depend on Int versus Float, while serialization must retain
any representation detail it is required to expose.

### Compatibility and backend support

This contract adds no syntax and changes no local evaluation semantics. It
defines a PostgreSQL translation rule for existing schemas and expressions.
Native JSON and JSONB remain ordinary application-readable formats. JSONB
metadata version 1 remains numeric-only. Plans outside the recoverable subset
remain Build errors rather than receiving guessed behavior.

## Alternatives considered

### Guess from the first matching schema alternative

This makes output depend on schema ordering rather than stored HFF identity
and can change dispatch and serialization. It is rejected.

### Encode every value with an application-level tag

A tagged document could make all alternatives explicit, but it changes the
stored JSON shape and ordinary PostgreSQL JSON access. It is outside this
design.

### Extend metadata version 1 to all HFF types

General type tags could distinguish values that share a JSON category, but
would require a broader storage and migration contract. The current metadata
format has a narrower numeric purpose. This design preserves that boundary
and leaves any format extension for a separate decision.

## Testing

The implemented JSON Duration subset has Build/unit coverage for
`Duration|Int|Null`, `Duration|Bool|Null`, `Duration|Int|Bool|Null`, and
`Duration|Float|Null` without numeric metadata, plus
`Duration|Int|Float|Null` with native JSON spelling and JSONB metadata.
PostgreSQL/local parity covers type inspection, guards, comparisons,
membership, list `contains()`/`unique()`, serialization, and null handling for
both temporal mixed-numeric variants in native JSON and JSONB with metadata.
Tests also cover Float signed zero, mixed-union lists, comparison with a
schema-known Float on plain JSONB, Build rejection for metadata-free JSONB
mixed-numeric operations that need a subtype, and ambiguous
`String|Duration` and `Bytes|Duration` operations that require the active
type. PostgreSQL/local parity cases cover supported scalar and list uses of
these disjoint unions.

Broader coverage for future union operations should compare PostgreSQL behavior
with local evaluation, including JSONB `Int|Float` with and without sufficient
metadata, JSON null versus absent members versus no-value, numeric boundaries,
negative zero when observable, and nested metadata pointer paths. Tests should
cover each newly supported operation—such as guards, comparisons, membership,
callbacks, collection construction, and `string()`/`json()`—rather than infer
support from category-disjointness alone. Build rejection tests should verify
that unsupported or ambiguous plans fail without database I/O. Stored metadata
consistency remains a caller contract, not a Build-time row-validation test.

## Limitations and open questions

This record defines when translation is safe; it does not promise that all
recoverable union operations can be lowered in one implementation stage. The
set of supported methods, collection shapes, and callback transformations
must be explicit in backend support documentation. JSONB metadata version 1
does not resolve string-category ambiguity for String, Bytes, and Duration.
Any future non-numeric metadata tags require a separate versioned-format
decision.
