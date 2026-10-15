# 058. Reuse uniform PostgreSQL item guards

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

PostgreSQL can lower object-item guards and scalar item-slot guard
normalization separately for each key. When those expressions produce
identical SQL, the per-key branches and their repeated dependencies do not
change the result. The builder can reuse the fallback expression instead, but
only after validating every callback and key in source order.

The equivalence test compares the generated SQL text for every guard matcher
or normalization expression, including the fallback. When they all match
exactly, the builder adopts the captured fallback SQL state and discards the
later dependencies. If any expression differs, it keeps the complete original
dispatch. This comparison does not parse SQL or infer equivalence from types.
It can collapse identical nonconstant checks as well as literal-false checks.
Source type alone does not exclude a match: some mixed-type guards also
produce identical SQL.

The helper is limited to item-pair and scalar item-slot guard matching and
guarded normalization. It keeps pair-null and outer-list checks, dynamic index
selection, presence and no-value state, Float recovery, and Build diagnostics.
General item-slot operations retain their existing lowering.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Item-pair or scalar item-slot guard with identical matcher SQL, including fallback | Applied | Reuse the fallback matcher after full validation. |
| Scalar item-slot guard normalization with identical SQL, including fallback | Applied | Reuse the fallback normalization expression after full validation. |
| Literal-false item matcher | Applied | The existing literal-false simplification remains available. |
| Nonuniform item matchers | Not applied | Preserve the full per-key dispatch and dependencies. |
| General item-slot operation | Not applied | The matcher helper is used only by guard paths. |
| Local backend | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [7283cee](https://github.com/ornew/hff/commit/7283cee97dacb42fedea0de82687abd4ddbfc236). The candidate SQL was generated from a working-tree implementation based on that snapshot; no candidate commit existed when these observations were collected. The generator uses Go 1.27.1 and the public HFF API. It emits `WHERE` text and ordered arguments for both PostgreSQL JSON formats; it does not execute queries or measure runtime performance.

Save the following program as `/private/tmp/hff-uniform-item-guard-audit.go`, then run it from the baseline and candidate checkouts. Change `before` to `after` in the output paths for the candidate.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	var fields []hff.Field
	for i := 0; i < 12; i++ {
		fields = append(fields, schema.Optional(schema.Nullable(
			schema.Bytes(fmt.Sprintf("label%02d", i)),
		)))
	}
	typ := schema.Struct(schema.Optional(schema.Nullable(
		schema.Field("labels", schema.Struct(fields...)),
	)))
	e, err := engine.New(
		engine.WithSchema(typ),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}

	sources := []string{
		`labels:items:any(p=>p:[list[string|null]]:exists)`,
		`labels:items:all(p=>p:[list[string|null]]:exists)`,
		`labels:items:filter(p=>p:[list[string|null]]:exists):size>0`,
		`labels:items:[list[list[string|null]]]:exists`,
		`labels:items[0]:[list[string|null]]:exists`,
		`labels:items:any(p=>p[1]:[string|null]:exists)`,
		`labels:items:all(p=>p[1]:[string|null]:exists)`,
		`labels:items:filter(p=>p[1]:[string|null]:exists):size>0`,
		`labels:items[0][1]:[string|null]:exists`,
		`labels:items:any(p=>p[1]:[null]=null)`,
		`labels:items:any(p=>p[1]:[bytes|null]:[string|null]:exists)`,
		`labels:items:any(p=>p[labels:size%3]:[string|null]:exists)`,
		`labels:items:any(p=>p[0]:[string]:exists)`,
		`labels:items:any(p=>p:[list[string]]:exists)`,
		`labels:items:any(p=>p:[list]:exists)`,
		`labels:items:map(p=>p[0]):size>0`,
	}

	var out []map[string]any
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		for _, source := range sources {
			program, err := e.Compile(context.Background(), source)
			if err != nil {
				panic(err)
			}
			query, err := hffsql.Build(context.Background(), program,
				hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(hffsql.BindJSON(
					hff.Path{"labels"},
					hffsql.Column("records", "labels"),
					format,
				)),
			)
			if err != nil {
				panic(err)
			}
			out = append(out, map[string]any{
				"source": source,
				"format": format,
				"where":  query.Where(),
				"args":   query.Args(),
			})
			fmt.Fprintf(os.Stderr, "%s %dB %dargs %s\n",
				format, len(query.Where()), len(query.Args()), source)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
```

For example, run from the baseline checkout with:

```sh
go run /private/tmp/hff-uniform-item-guard-audit.go \
  > /private/tmp/hff-uniform-item-guard-before.json \
  2> /private/tmp/hff-uniform-item-guard-before.log
```

The input schema is an optional, nullable `labels` Struct with twelve optional,
nullable Bytes fields named `label00` through `label11`. The query matrix
covers pair, value-slot, indexed, chained, dynamic-index, null, and key-only
guards, along with literal-false and unconstrained controls. Both JSON and
JSONB bindings use `records.labels`.

## Results

The observations report generated `WHERE` size and argument count. They are
SQL-shape measurements, not execution-time or runtime-performance results.

| Guard shape | JSON before → after | JSONB before → after |
|:--|:--|:--|
| Pair `any()` with uniform nonconstant matcher | 15,760 → 12,056 B; 24 → 12 args | 15,789 → 12,085 B; 24 → 12 args |
| Value-slot `any()` | 15,812 → 12,000 B; 37 → 13 args | 15,841 → 12,029 B; 37 → 13 args |
| Value-slot `filter()` | 16,247 → 12,435 B; 38 → 14 args | 16,276 → 12,464 B; 38 → 14 args |
| First value slot | 15,585 → 11,797 B; 38 → 14 args | 15,614 → 11,826 B; 38 → 14 args |
| Null slot guard | 15,663 → 11,983 B; 37 → 13 args | 15,692 → 12,012 B; 37 → 13 args |
| Chained Bytes/null then String/null guard | 18,322 → 14,510 B; 49 → 25 args | 18,351 → 14,539 B; 49 → 25 args |
| Dynamic index slot | 16,990 → 13,178 B; 37 → 13 args | 17,021 → 13,209 B; 37 → 13 args |

The query matrix also included literal-key, literal-false, `Any`, key-only,
and mixed String/Bytes controls. Their SQL and arguments remain unchanged when
their per-key matchers are not identical. A few mixed-source cases do qualify
when their generated matchers are identical; source-type labels alone do not
determine applicability.

The raw generated SQL and logs are kept outside the repository at
`/private/tmp/hff-uniform-item-guard-{before,after}.{json,log}`. All 32 outputs
for each revision matched the recorded SQL and arguments.

## Validation and plan observations

The related PostgreSQL suite passed with 253,804 logged query-record outputs
across 719 groups. This includes 18,428 outputs across 36 groups for the
34-source uniform-matcher matrix; 32 additional Float-boundary outputs checked
null-exists, null-type, and null-equality behavior. These are selected parity
checks, not a claim of full backend conformance. Root unit tests and vet,
integration vet, SQL race tests, and the 19 native plus 5 JavaScript WebAssembly
checks also passed.

Plain `EXPLAIN (FORMAT JSON, COSTS OFF)` on PostgreSQL 18.6, with JIT disabled
and a 30-second statement timeout, showed unchanged plan-node counts for four
JSON queries over `records(labels)`. The fixture row was
`'{"label00":"YQ==","label01":null}'::json`; the queries used the bind
arguments emitted by the generator. Pair `any()` retained two Aggregate and
three Function Scan nodes; value-slot `any()` and the chained slot guard each
retained two Aggregate and three Function Scan nodes; the dynamic-index guard
retained three Aggregate and four Function Scan nodes. These plan observations
do not show a reduction in plan-node counts or runtime cost. No timings,
allocations, or `EXPLAIN ANALYZE` results were collected.

## Correctness and tradeoffs

The fallback SQL and builder state are captured after fully lowering the
fallback callback or normalization expression. Every later key and callback
is still validated in source order, including UTF-8/NUL key checks and guard
type checking. Uniformity reuses only the fallback state; nonuniformity keeps
the original full dispatch. Thus key-specific parameters are discarded only
when their matcher or normalization SQL is identical to the fallback.
Pair-null checks, outer-list checks, empty-list behavior, callback scope,
null/no-value distinctions, and Float recovery remain unchanged.

Build copies builder state and compares the generated expressions; its cost was
not measured. The reduction in SQL text and arguments does not establish a
runtime speedup.

## Decision and limits

Apply matcher and normalization reuse only after exact SQL-text equality with
the fallback has been established for every validated branch. Keep the full
per-key dispatch for any nonuniform expression or operation outside the
dedicated guard paths.
