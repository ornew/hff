# 038. Remove projections from eligible native-column filters

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Some filters over directly bound PostgreSQL scalar columns were translated
through nullable value projections even when the operation could be evaluated
on the column itself. For eligible filter expressions, the translator now
emits direct predicates and keeps explicit non-null checks where the HFF value
is nullable. A required native field's `exists()` check becomes `TRUE` without
a value projection; nullable native fields use `IS NOT NULL`.

The covered forms include direct `exists()` and same-kind native-column
comparisons; `len()` on a native `bytea` compared with an Int literal; the
Int-valued `Duration:nanoseconds()` comparison against a native Int column;
and eligible Int `round()`, `ceil()`, and `floor()` identity operations.
Previously supported direct identity rewrites remain eligible. These changes
remove intermediate projections while preserving outer-filter null behavior,
parameter order, and the SQL value type.

The rewrite is limited to the proven native scalar forms. Guard-presence
tests, Float-versus-Int comparisons, JSON paths, structured-parent state,
and callback bodies keep their general lowering. Other qualified-column and
identity projection opportunities remain outside this record.

## Applicability

| Filter form | Status | Reason |
|:--|:--|:--|
| `i:exists` on a required native scalar | Applied | The schema guarantees a non-null value, so the predicate becomes `TRUE`. |
| `i:exists` on a nullable native scalar | Applied | Emits an `IS NOT NULL` predicate. |
| Same-kind native scalar columns such as `i=j` or `s<t` | Applied | Emits direct column comparisons with nullable masks. |
| Native `bytea` `len()` compared with an Int literal | Applied | Emits `octet_length(bytea)` and retains the receiver-null mask. |
| `d:nanoseconds` compared with an Int column | Applied | The result is already an Int carrier and needs no value projection. |
| Int `round()`, `ceil()`, or `floor()` in an eligible filter | Applied | These operations preserve the Int value and subtype. |
| Guarded, structured-parent, JSON, mixed-kind, or callback forms | Not applied | Their presence, representation, or scope still requires general lowering. |

## Reproduction

The baseline is commit
[2b3b1be](https://github.com/ornew/hff/commit/2b3b1be1b6267895b3f4090c3fea3612f7df5924).
The checks used Go 1.27.1 on Darwin/arm64 and PostgreSQL 18.6 on Debian/aarch64
with `extra_float_digits=1`. Float handling follows the PostgreSQL Profile;
this work does not change that contract.

The shape fixture used this temporary table:

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

The corresponding HFF schema used Int for `i`/`j`, Float for `f`/`g`, String
for `s`/`t`, Bool for `p`/`q`, Bytes for `b`/`c`, and Duration for `d`/`e`.
Each field used `Bind(hff.Path{name}, Column("records", name))`, with
Duration stored as bigint nanoseconds. The fixture tested each scalar as
required and nullable; `items` was `List<Int>` and `data` was `Map<Any>`, both
bound with `BindJSON(..., JSON)`. The temporary shape harness measured the `WHERE` byte length,
`OFFSET 0` count, and argument count for these representative queries:

```hff
i:exists
i=j
s<t
b:len>0
d:nanoseconds=i
i:floor>1
i:exists b:len>0 d:nanoseconds=i
```

The durable integration fixture
[`scalar_column_where_integration_test.go`](https://github.com/ornew/hff/blob/main/backends/sql/scalar_column_where_integration_test.go)
compares SQL with local evaluation over required, nullable, and optional
profiles. It covers Int, Float, String, Bool, Bytes, and Duration values;
nulls, omitted optional fields, signed zero, numeric boundaries, byte contents,
string ordering, callback controls, and mixed-kind fallback. The same 30,740
local-oracle comparisons passed against the baseline and candidate.

Run the focused checks with:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql -run '^TestNativeColumnFilters'
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql -run '^TestPostgreSQLNativeColumnFiltersMatchLocal$'
```

The temporary shape generator and emitted SQL are not committed; the table
and query list above describe the measured inputs. The committed integration
test is the reproducible semantic oracle.

## Results

The table reports generated `WHERE` bytes and `OFFSET 0` barriers. These are
SQL-shape measurements, not execution timings.

| Workload | Filter | Baseline bytes / barriers | Candidate bytes / barriers |
|:--|:--|--:|--:|
| Nullable existence | `i:exists` | 173 / 2 | 27 / 0 |
| Same-kind Int comparison | `i=j` | 261 / 3 | 91 / 0 |
| Same-kind String comparison | `s<t` | 285 / 3 | 115 / 0 |
| Bytes length | `b:len>0` | 282 / 3 | 82 / 0 |
| Duration nanoseconds | `d:nanoseconds=i` | 328 / 4 | 91 / 0 |
| Int identity | `i:floor>1` | 260 / 3 | 60 / 0 |
| Composed filters | `i:exists b:len>0 d:nanoseconds=i` | 820 / 9 | 212 / 0 |
| Required existence | `i:exists` | 173 / 2 | 4 / 0 |
| Direct null-test control | `i!=null` | 27 / 0 | 27 / 0 |
| Mixed Int/Float control | `i=f` | 867 / 5 | 867 / 5 |
| Callback control | `items:any(x=>i:exists)` | 679 / 6 | 679 / 6 |

Candidate plain `EXPLAIN` on the empty fixture showed the optimized filters
as direct scan predicates without `SubPlan` or `Nested Loop` nodes; required
`exists()` needed no filter. This plan observation is fixture-specific and
does not measure execution or promise a plan for populated data.

## Correctness and tradeoffs

Paired local/PostgreSQL comparisons passed for both revisions. Nullable
columns retain explicit non-null checks, so applying prefix `!` preserves the
ordinary HFF truth rule for a false-on-null predicate. Required `exists()` is
folded only when the schema guarantees a non-null value. Same-kind comparisons
retain the database column's carrier; String comparisons explicitly use
`COLLATE "C"` for HFF code-point ordering. `bytea` length uses
`octet_length`; Duration nanoseconds already have an Int carrier. Int
`round()`, `ceil()`, and `floor()` remain identity operations.

The candidate also preserves surrounding argument order. Guarded checks,
structured-parent presence, JSON recovery, mixed numeric comparisons, and
callback scopes retain the existing lowering because those cases require
additional value or presence state.

## Decision and limits

Use direct predicates for the eligible native-column forms above. The change
reduces generated SQL and removes projection barriers in those cases, but no
runtime speed, allocation, or general plan improvement was measured. Broader
qualified-column projection elimination remains separate work.
