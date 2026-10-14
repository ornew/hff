# 040. Reuse repeated native operations within a projection scope

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Repeated native Int arithmetic and String case conversion could translate and
project the same computation more than once in a PostgreSQL filter or callback
scope. The translator can now reuse a completed native result and its presence
for Int addition, subtraction, multiplication, division, modulo, negation, and
built-in String `lower()` and `upper()` when the ordered operation and typed
operands match within the existing projection scope.

Each occurrence is lowered and validated in source order before the lookup.
Cache keys include the operation, result type, ordering class, and each
operand's kind, presence, static type, and carrier. Int and String literal
nodes, plus built-in conversions already folded by ordinary lowering, use
their validated semantic values rather than fresh placeholder positions. On
a cache hit, only arguments belonging to newly translated eligible literal
operands are removed. A hit involving a nonliteral operand is allowed only
when lowering that operand added no arguments. A cache miss can register a new
result for reuse by a later nested expression.

## Applicability

| Repeated expression | Status | Reason |
|:--|:--|:--|
| Same native Int `+`, `-`, `*`, `/`, `%`, or negation with matching typed operands | Applied | Reuses the completed value/presence projection in the same scope. |
| Same built-in String `lower()` or `upper()` with matching receiver and operands | Applied | Reuses the translated case-conversion result and its arguments. |
| Nested repeats in one callback or fold scope | Applied | Reuses results only within the existing scope and binding identity. |
| Different operands, operator, receiver, presence, type, or scope | Not applied | The cache key does not match; ordinary lowering remains. |
| Custom method translators | Not applied | They are invoked through the normal extension contract and are never constant-evaluated for cache keys. |
| A nonliteral cache hit that would add new parameters | Not applied | Its new arguments cannot be discarded safely. |

## Reproduction

The baseline is commit
[2f47561](https://github.com/ornew/hff/commit/2f4756148c121dd8aad02c659ef8bc9583fe6648).
The fixture used Go 1.27.1 on Darwin/arm64 and PostgreSQL 18.6 on
Debian/aarch64 with `extra_float_digits=1`. It measured generated `WHERE`
bytes, `OFFSET 0` barriers, and parameter counts; no execution timing or
benchmark was collected.

The parity fixture bound native Int fields `i` and `j`, Float fields
`f` and `g`, String fields `s` and `t`, Bool fields `p` and `q`, Bytes fields
`b` and `c`, and Duration fields `d` and `e` (bigint nanoseconds), all nullable.
It also bound `items` as `List<Int>`, `data` as `Map<Any>`, and `words` as
`List<String|null>` in JSON. The identical
fixture compared 40 filters over 121 records (11 values for each Int input),
including null, `int64` limits, negative and zero values, Unicode case ranges,
signed zero, division/modulo failures, nested arithmetic, callback captures
and shadowing, maps, and folds. All 4,840 PostgreSQL/local comparisons passed
on both baseline and implementation.

The temporary shape fixture used this table and qualified native bindings:

```sql
CREATE TEMP TABLE records (
  i bigint, j bigint,
  f double precision, g double precision,
  s text, t text,
  p boolean, q boolean,
  b bytea, c bytea,
  d bigint, e bigint,
  items json, data json
);
```

The HFF bindings used nullable and required profiles for the scalar pairs;
`items` was `List<Int>` and `data` was `Map<Any>`, each bound with
`BindJSON(..., JSON)`. Duration fields use bigint nanoseconds.

The durable parity fixture is
[`native_operation_cache_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/native_operation_cache_integration_test.go).
Unit fixtures cover argument ordering, Unicode conversion, cache scope,
extension dispatch, text validation, and parameter-use integrity. Run them
with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestNativeOperation'
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLNativeOperationReuseMatchesLocal$'
```

The separate temporary shape fixture used the same nullable and required
column bindings, plus `List<Int>` and `Map<Any>` JSON bindings. Representative
filters include:

```hff
i+2>10 i+2<20
i+j>0 i+j<20
i/2>0.0 i/2<20.0
(-i)>0 (-i)<20
(i+2)*3>10 (i+2)*3<20
s:lower="a" s:lower="b"
s:lower:upper="A" s:lower:upper="B"
items:any(x=>x+2>0 x+2<20)
items:foldr(i,(x,i)=>(i+x)*2+(i+x)*2)>0
```

The temporary generator and emitted SQL are not committed. The integration
fixture is the durable semantic oracle; the filters and binding families
above describe the SQL-shape inputs.

## Results

Generated `WHERE` byte counts, `OFFSET 0` barriers, and arguments were counted
for the same representative filters on baseline and implementation. The table
reports the nullable scalar profile and measures SQL shape only.

| Filter | Baseline bytes / barriers / args | Implementation bytes / barriers / args |
|:--|--:|--:|
| `i+2>10 i+2<20` | 1,050 / 7 / 4 | 715 / 5 / 3 |
| `i+j>0 i+j<20` | 1,052 / 7 / 2 | 716 / 5 / 2 |
| `i/2>0.0 i/2<20.0` | 742 / 5 / 4 | 571 / 4 / 3 |
| `(-i)>0 (-i)<20` | 644 / 5 / 2 | 494 / 4 / 2 |
| `(i+2)*3>10 (i+2)*3<20` | 1,752 / 11 / 6 | 1,064 / 7 / 4 |
| `s:lower="a" s:lower="b"` | 708 / 5 / 6 | 566 / 4 / 4 |
| `s:lower:upper="A" s:lower:upper="B"` | 1,021 / 7 / 10 | 722 / 5 / 6 |
| `items:any(x=>x+2>0 x+2<20)` | 1,800 / 13 / 4 | 1,464 / 11 / 3 |
| `items:foldr(i,(x,i)=>(i+x)*2+(i+x)*2)>0` | 3,500 / 20 / 3 | 2,807 / 16 / 2 |
| Different Int constants or operators control | 1,050 / 7 / 4 | unchanged |
| Single-use arithmetic control | 477 / 3 / 2 | unchanged |
| Single-use `lower()` control | 306 / 2 / 3 | unchanged |

Plain `EXPLAIN` on an empty table showed fewer nested-loop nodes in sampled
repeated expressions: six to four for the root arithmetic example, ten to six
for the nested arithmetic example `(i+2)*3>10 (i+2)*3<20`, and four to three
for repeated `lower()`. These
fixture-specific plans were inspected without `ANALYZE`; they do not establish
execution speed or a plan for populated tables.

## Correctness and tradeoffs

All operands are lowered and validated before the lookup, preserving source
order and diagnostics. Tests assert that every retained SQL placeholder has
an argument and every argument remains referenced. When a cache hit removes a
literal's fresh placeholder, the first occurrence's parameter remains in its
original position. Nonliteral cache hits that would add arguments fall back
to ordinary lowering. Unicode coverage includes all Unicode case ranges,
Turkish I, sigma variants, sharp S, and supplementary characters.

Different operators, constants, receivers, guarded-presence states, and
callback scopes retain separate computations. Callback captures may reuse a
value inside their scope, while shadowed bindings and separate callbacks do
not share entries. Custom method implementations are invoked normally and are
not evaluated to discover constant cache inputs. The tests verify that no
unused placeholders remain.

## Decision and limits

Reuse only completed native Int arithmetic/negation and built-in String case
conversion results whose operation, typed operands, presence, result type, and
projection scope match. The reduced SQL shape is not a runtime-speed claim.
Other computed methods, custom translators, and cross-scope reuse retain their
ordinary lowering.
