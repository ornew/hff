# 039. Reuse qualified raw native columns in generic expressions

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Generic scalar expressions could add a `LATERAL` value projection even when a
qualified native scalar binding already supplied the exact SQL column
expression. Build now records a narrow proof for a direct scalar `Bind` whose
SQL column reference has at least two literal identifier components. If a
later Variable or Field node resolves to that exact registered column and has
constant `TRUE` presence, the expression can reuse the column in the current
projection scope.

Presence here means the internal no-value flag, not SQL nullability. Required,
nullable, and optional scalar fields can all qualify: SQL NULL remains NULL,
and consumers still apply their normal null rules. A guard's result keeps its
own no-value handling even when its raw child column can be reused.

The proof does not apply to unqualified column references, guard results,
`BindJSON`, Timestamp conversions, metadata, `BindStruct` parent-state
expressions, `BindComposite`, `BindRelation`, computed expressions, or callback
parameters. Those keep their existing projections and presence handling.
A plain `Bind` for a field beneath required schema structs can qualify.
Path caches remain isolated by callback scope and binding identity, so
captures never replace shadowed parameter bindings.

## Applicability

| Expression | Status | Reason |
|:--|:--|:--|
| Qualified native scalar `Bind` with an exact raw Variable or Field reference | Applied | Reuses the column with constant `TRUE` no-value presence, including nullable and optional fields. |
| Qualified raw column inside an eligible composed expression or callback capture | Applied | Existing projection-scope caches preserve binding identity and scope. |
| Unqualified references, computed method results, or guard results | Not applied | Keeps the existing binding, computed-value, or no-value projection. |
| JSON, metadata, Timestamp, structured-parent, relation, or callback-parameter values | Not applied | These are converted, stateful, or scoped carriers rather than the registered raw scalar. |

## Reproduction

The baseline is commit
[2ac11c1](https://github.com/ornew/hff/commit/2ac11c13c7a44ecc91dad9be32eabf83421d2755).
The shape checks used Go 1.27.1 on Darwin/arm64 and PostgreSQL 18.6 on
Debian/aarch64 with `extra_float_digits=1`. They measured generated `WHERE`
bytes and `OFFSET 0` barriers, not execution time.

The shape fixture used the following empty temporary table:

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

The HFF schema used Int for `i`/`j`, Float for `f`/`g`, String for `s`/`t`,
Bool for `p`/`q`, Bytes for `b`/`c`, and Duration for `d`/`e` (bigint
nanoseconds). Each scalar used `Bind(hff.Path{name}, Column("records", name))`.
All scalars were tested as nullable and, separately, required. `items` used
`List<Int>` and `data` used `Map<Any>`, both bound with `BindJSON(..., JSON)`.
Representative filters were `i+1>j`, `i=f`, `i!:exists`, `s:lower=t`,
`b:len=c:len`, `items:any(x=>i:exists)`, and
`i+1>0 j+2<10 s="end"`.

The durable integration fixture
[`native_column_projection_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/native_column_projection_integration_test.go)
checks qualified identifiers, reserved-looking aliases and names, unqualified
fallbacks, structured-parent state, callback captures and shadowing, and
computed-expression controls. In addition to the earlier direct-filter
matrix's 30,740 comparisons, 1,856 scoped comparisons passed against both
baseline and candidate. The scope fixture has 29 filter/binding cases over
64 records (four left Int values, four right Int values, and four parent
states: missing, null, zero age, and nonzero age). Its qualifier is
`__hff_1`; physical names include `v`, `p`, `value`, `ord`, and a name
containing a quote and dot. Unqualified `v`/`p` bindings are separate controls.
The existing Float operand controls (168 comparisons), relation-alias controls
(20 pairs), and repeated numeric-kernel controls (16,416 comparisons) also
passed against both revisions.

Run the dedicated fixture with:

```sh
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLQualifiedNativeColumnScopes$'
```

The temporary shape generator and emitted SQL are not committed. The schema,
binding shape, and representative filters above describe the measured cases;
the integration fixture is the durable semantic oracle.

## Results

The table reports nullable-field `WHERE` byte lengths and `OFFSET 0` barrier
counts before and after. Reductions are partial: operation projections and
other barriers remain when an expression computes a new value.

| Filter | Baseline bytes / barriers | Candidate bytes / barriers |
|:--|--:|--:|
| `i+1>j` | 610 / 5 | 478 / 3 |
| `i=f` | 867 / 5 | 751 / 3 |
| `i!:exists` | 290 / 3 | 225 / 2 |
| `f:floor>1` | 880 / 5 | 813 / 4 |
| `i:json=i:json` | 396 / 4 | 331 / 3 |
| `s:lower=t` | 441 / 4 | 309 / 2 |
| `b:len=c:len` | 439 / 5 | 305 / 3 |
| `d:nanoseconds:json=i:floor:json` | 599 / 7 | 465 / 5 |
| `items:any(x=>i:exists)` | 679 / 6 | 612 / 5 |
| `i+1>0 j+2<10 s="end"` | 1,197 / 8 | 1,067 / 6 |
| JSON field `data.i:exists` control | 523 / 5 | 523 / 5 |
| Direct `i=j` control | 91 / 0 | 91 / 0 |

Plain `EXPLAIN` on the fixture showed fewer nested-loop layers for affected
expressions, for example four to two for `i+1>j`. The plans were inspected on
empty fixtures without `ANALYZE`; no runtime speed claim follows.

## Correctness and tradeoffs

The translator reuses a column only when the binding path resolves to the
exact SQL expression registered during `Build`. Literal identifier components
are preserved and quoted, and generated aliases avoid caller-owned names.
Nullable raw columns keep SQL NULL without needing a leaf projection. A guard
result, structured parent, JSON conversion, or computed method result does
not acquire the raw-column proof. Callback
captures can reuse an outer bound column only within the existing scope; a
callback parameter or shadowed binding does not inherit that proof.

This removes projections for eligible raw references inside generic
expressions. It does not eliminate projections for the operations around them:
for example, `i+1`, `s:lower`, `b:len`, and `f:floor` still compute values and
retain the barriers required by their current lowering. SQL shape and plan
observations do not measure execution speed or guarantee plans for other
schemas and data.

## Decision and limits

Reuse exact qualified raw scalar columns only with the recorded path, SQL
expression, and constant-presence proof. Keep the general lowering for
unqualified, computed, converted, guarded, structured, and otherwise scoped
values until each has a separate correctness proof.
