# Capabilities and Backends

## One syntax, selectable functionality

A service configures the features that its filters may use. This includes
available methods; the configuration model must also cover selectable language
operations as the implementation is designed.

Structural equality uses `comparison:list-equality` and
`comparison:object-equality` permissions. `group:structural-equality` selects
both. Compilation checks only structural value families that the static types
can actually compare, recursively through compatible nested lists and objects.
Scalar comparisons and explicit comparisons with a null literal are exempt.
The permissions apply to `=`, `!=`, membership (`in()` and
`list:contains()`), and `list:unique()`. A list of scalar values therefore
does not need structural equality for `list:unique()`, while comparing the list
value itself does.

For `Any` and union operands, compilation checks all statically possible
structural comparisons. An explicit type guard narrows those possibilities and
can avoid requiring a permission for excluded value families. For example,
`value:[list[string]]:unique` needs no structural equality permission, while
`value:[list[string]]=["a"]` requires list equality because the list values are
compared. A structural comparison without its required permission is a
compile-time capability error; runtime values do not turn a disabled structural
operation into null.

The parser MUST recognize the same syntax for every configuration. A statically
known operation outside the enabled set MUST be rejected during semantic
compilation. For a call on a union or `Any`, every statically applicable method
signature must have its capability enabled. An explicit type guard can narrow
the receiver first, so only signatures applicable to the narrowed type require
permission. A disabled operation is a compile-time capability error; runtime
values do not turn a disabled operation into null or a runtime capability error.
Disabled features must not be silently ignored, rewritten to true, or
interpreted as different syntax.

HFF aims to ship engines that services can use by supplying a schema and a
capability selection. Backend support is extensible without introducing a
service-specific grammar. The [Go API record](../docs/design/004-go-api.md) defines configuration, common
compilation, and separate backend construction. The implemented configuration
APIs are described in the [Go guides](../docs/guides/README.md).

## Compilation

The intended compilation flow is:

1. Parse the entire query with the PEGO-generated typed Go parser.
2. Resolve variables and fields against the supplied schema.
3. Validate operator and method types and the field-related filter restriction.
4. Check enabled capabilities and produce a backend-independent typed program.
   For union/`Any` calls, require every statically applicable method signature;
   explicit type guards may narrow the receiver before this check.
5. Build the selected backend artifact, checking its support without constructing
   artifacts for other backends.

These are responsibilities, not a mandated implementation pass order.
Diagnostics should identify the relevant source expression and explain syntax,
schema, type, capability, or backend failures. A service can constrain its
supported feature set without maintaining a fork of the syntax.

Diagnostics SHOULD include a stable category, a source span, and a concise
explanation. Type diagnostics SHOULD report expected and inferred types;
capability diagnostics SHOULD identify the unavailable operation. Source spans
refer to the original UTF-8 query, before optimization. Human-facing locations
SHOULD additionally report a line and column with their counting convention.
Input-validation failures and evaluation cancellation MUST be distinguishable
from compilation diagnostics and completed filter results.

## Runtime purity and portability

The runtime MUST NOT mutate input data or expose I/O, network access, assignment,
or other side effects through filters. Methods that produce lists or objects
return values without changing their receivers.

Evaluation must use explicit host data and configuration. Time zone methods must
not depend on the machine's local zone. Precision, Unicode behavior, time zone
data, and resource limits need portable contracts before cross-runtime conformance
can be claimed. Runtime size and performance targets remain to be measured.

Type safety includes checked dynamic dispatch: a union or `Any` must not allow
an incompatible runtime operation to escape as an unchecked host-language call.
See [types](types.md) and [null handling](evaluation.md).

For a statically known receiver, every applicable method requires its capability
to be enabled. A union or `Any` may have applicable signatures from several
method families; every statically applicable signature must be enabled, even if
some runtime values cannot use it. An explicit type guard narrows the receiver
and permits a call whose applicable signatures are enabled for that narrowed
type. A type guard succeeds by retaining the original value when it matches and
produces internal no-value otherwise. This lets a service disable list
containment while still allowing string containment after a string type guard.
For a receiver known only as null, no non-null family selects a method, so all
known applicable candidates require permission. A `T|null` receiver selects
method candidates from its non-null `T` members; null still propagates normally.
See [types](types.md), [syntax](syntax.md), and [evaluation](evaluation.md).

Structural equality permissions are checked for `=`, `!=`, `in()`,
`list:contains()`, and `list:unique()`. The compiler recursively checks
compatible composite types that can be compared. `Any` and unions require each
statically possible list or object permission, while explicit type guards
narrow the checked types. Scalar comparisons and explicit null tests need no
structural equality permission. `group:structural-equality` selects both
`comparison:list-equality` and `comparison:object-equality`.

## Backend translation

A backend compiles HFF's meaning, not merely its operator spelling. SQL is an
example target, not the definition of the language.

In particular, a translator must account for:

- HFF's AND-before-OR precedence and boolean conversion.
- Explicit null tests and false results for other null comparisons.
- Safe field access and the predicate guarantees of guarded paths.
- RE2 matching, list indexing, type behavior, and supported methods.

SQL's three-valued null logic cannot be copied directly without verifying the
HFF result. A backend that cannot preserve the semantics of a requested feature
must reject it as unsupported. Capability selection should let a service expose
the features that its chosen target can implement faithfully.

Unsupported backend features are compile-time errors. There is no implicit
local fallback or hybrid execution. SQL parameter binding and identifier mapping
need explicit integration contracts before a SQL backend is implemented.

Services configure resource limits. Exceeding a runtime limit returns an explicit
evaluation-cancellation result rather than null or a partial boolean result.
Compiler limits, including `MaxCompileSteps` and `MaxCallbackDepth`, reject the
query before execution. `MaxSteps` and `MaxGeneratedValues` are local runtime
budgets. A backend reports the execution constraints it can enforce; callers
configure database timeouts through their database integration. If a requested
constraint cannot be enforced by the selected backend, it must reject the build
instead of silently ignoring it. Exact default limit values belong to engine
configuration, not to a syntax subset.

## Conformance and discoverability

An engine SHOULD expose its supported operations, method signatures, configured
limits, and backend restrictions to host applications. Parsing success alone
must never be presented as proof that an operation is supported.

Conformance fixtures SHOULD specify the query, schema, input bindings, enabled
features, and expected outcome. Outcomes include compilation rejection, input
validation rejection, cancellation, and a completed boolean result. Fixtures
for value operations additionally record the result type and value, distinguishing
null from guarded no-value. Standalone constant subexpressions in these fixtures
test value semantics and do not relax the field-related filter restriction.

Local engines and backend adapters SHOULD share semantic fixtures. The suite
must cover numeric boundaries, null comparisons, guarded paths, missing keys,
Unicode, time conversion, and short-circuiting. Optimized and unoptimized local
plans MUST produce the same completed results. Resource accounting and execution
constraints require separate tests for the guarantees each engine advertises.

## Extension contract

Extension methods use the existing receiver-call syntax. Each immutable
declaration describes one stable signature ID, receiver type, required and
optional parameter types, an optional variadic type, result type, null policy,
and fixed base cost. `engine.WithMethods` configures declarations;
`capabilities.Method(declaration.ID())` separately grants permission. A local
evaluator is registered with `local.WithMethods(local.Implement(...))`. Required
parameters precede optional positions and the variadic tail; optional positions
cannot be skipped to supply a variadic argument. Non-overlapping signatures may
share a call name; duplicate IDs, built-in collisions, and ambiguous signatures
are rejected.

Extensions MUST be pure, respect null/no-value rules, and participate in
capability and backend checks. The local evaluator has a fixed base cost of at
least one unit and can charge additional steps and generated values through its
call meter. Null propagates without invocation by default; explicit null
invocation requires nullable declared types. No-value always propagates and is
never exposed as an HFF value. Implementations MUST NOT replace a built-in method
name. Adding an extension does not introduce tokens, operators, statements, or
a service-specific grammar. PostgreSQL extensions use separately registered
dialect translators. Each used custom signature needs an exact PostgreSQL
implementation; missing translators or unsupported SQL semantics reject Build
with a backend error. Translators are limited to the backend's supported scalar
expression types and do not extend its support to collections, JSON, or temporal
values.

Engines identify their Unicode case-data, regex compatibility, and time zone
data versions. HFF's regex reference follows RE2 except for the excluded `\C`
byte escape. A backend may advertise a smaller supported regex subset; its
support matrix or compatibility profile must state restrictions explicitly.
These identifiers belong to the advertised compatibility profile; the language
does not prescribe a particular library version. Services comparing results
across engines must compare the relevant profiles as well as features.

The local backend obtains named-zone data from an explicit versioned TZif
profile supplied at Build. It copies and parses the profile once; matching does
not consult the operating system's zone files or environment. UTC and fixed
offsets are built in, while unavailable named zones yield null. The filter
reports the caller-supplied version label, which identifies but does not
authenticate the data's provenance.
