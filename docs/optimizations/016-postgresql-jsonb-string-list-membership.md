# 016. Emit direct membership predicates for JSONB String lists

- **Status**: Implemented
- **Date**: 2026-10-14

## Problem and mechanism

Membership on a JSONB list of strings can pass through several intermediate
projections even when the binding declares a direct JSONB `List<String>` and
all needles are literal Strings. For this narrow shape, the implementation
emits JSONB containment directly in the filter:

```sql
("records"."tags" IS NOT NULL AND
  "records"."tags" @> jsonb_build_array(($1::text)))
```

Using `@>` can expose the predicate to PostgreSQL's JSONB GIN operator classes.
The needles remain bound parameters rather than interpolated. With multiple
needles, one JSON array is built and every requested string must occur in the
bound list. Duplicate needles do not require duplicate list entries.

For example, `tags:contains("auth","dev")` tests whether both strings occur;
the SQL uses one `jsonb_build_array` expression with two bound text values:

```sql
"records"."tags" @> jsonb_build_array(($1::text), ($2::text))
```

## Applicability

| Execution profile | Status | Reason |
|:--|:--|:--|
| Direct physical JSONB `List<String>` binding with one or more literal String needles | Applied | Emits containment without projection barriers. |
| Nullable JSONB container with one or more literal String needles | Applied | The direct predicate includes a non-null test. |
| Null/dynamic needles, guards, metadata-backed or plain JSON, nested/Any/structural lists, or nullable elements | Not applied | These shapes retain the general lowering. |

## Reproduction

The comparison measures the one-needle HFF filter `tags:contains("auth")`
and a direct physical binding of `tags` to the JSONB column `records.tags`,
declared as `List<String>`. The baseline is commit
[a498cde](https://github.com/ornew/hff/commit/a498cde); the implementation is in
`backends/sql/direct_jsonb_membership.go` and its focused tests. The lengths
below are generated `WHERE` expression character counts; barriers count
`OFFSET 0` projections in the expression. One bound text parameter is used
in both forms.

For a plan-shape check, the direct predicate was prepared and explained
against an empty temporary `hff_candidate_tags` table. Separate indexes using
`jsonb_ops` and `jsonb_path_ops` were inspected with default planner settings.

Targeted unit and local/PostgreSQL parity checks passed for SQL NULL and JSON
null inputs, empty lists, duplicate entries, Unicode and quoted/backslash
strings, negation, conjunction/disjunction, and fallback observations. Full
`go test ./...` and `go vet ./...` passed, and independent critical review
found no material issue.

## Results

| HFF filter | Prior WHERE length / barriers / parameters | Direct WHERE length / barriers / parameters |
|:--|--:|--:|
| `tags:contains("auth")` | 1,164 characters / 4 / 1 | 84 characters / 0 / 1 |

Plain `EXPLAIN` selected a Bitmap Index Scan with either GIN operator class;
the index condition was `tags @> jsonb_build_array('auth'::text)`. This
observation is limited to the empty temporary fixture and default planner
settings. No `EXPLAIN ANALYZE` or execution timing was collected. The shorter
SQL and this plan do not establish a runtime speedup.

## Correctness and tradeoffs

The path is limited to a directly bound JSONB `List<String>` and one or more
literal String needles. It preserves nullable-container filtering and leaves
null needles, dynamic expressions, guards, metadata-backed values, plain JSON,
nested or structural lists, `Any` elements, and nullable element types on the
general lowering path. Targeted parity covers null distinctions, duplicate
strings, Unicode, quoted/backslash content, boolean negation/composition, and
representative fallback expressions.

## Decision and limits

This implementation changes SQL construction only for the eligible list and
needle shape. Whether `@>` uses a GIN index depends on the operator class,
schema, statistics, and data. The fixture plan does not establish production
index selection or execution performance. Validate generated SQL and plans
against representative indexed tables before relying on index use.
