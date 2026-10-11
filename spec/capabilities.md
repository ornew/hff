# Capabilities and Backends

## One syntax, selectable functionality

A service configures the features that its filters may use. This includes
available methods; the configuration model must also cover selectable language
operations as the implementation is designed.

The parser MUST recognize the same syntax for every configuration. A feature
outside the enabled set MUST be rejected during semantic compilation. Disabled
features must not be silently ignored, rewritten to true, or interpreted as
different syntax.

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
Compiler limits reject the query before execution. Exact default limit values
belong to the engine configuration, not to a syntax subset.

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

Extension methods use the existing receiver-call syntax and declare their
receiver type, parameter types and arity, result type, and resource accounting.
They MUST be pure, respect null/no-value rules, and participate in capability and
backend checks. They MUST NOT replace a built-in method name. Adding an extension
does not introduce tokens, operators, statements, or a service-specific grammar.
An extension requiring unavailable processing-system behavior is rejected.

Engines identify their Unicode case-data, RE2 compatibility, and time zone data
versions. These identifiers belong to the advertised compatibility profile;
the language does not prescribe a particular library version. Services comparing
results across engines must compare the relevant profiles as well as features.
