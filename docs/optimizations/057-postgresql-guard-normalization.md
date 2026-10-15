# 057. Reuse proven PostgreSQL guard-normalized JSON

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

Two PostgreSQL JSON paths can emit work whose result is already known or whose
representation is already in the required form. These rewrites remove only
work proven redundant after complete validation.

For a recovered object-item guard, the builder lowers and validates the full
operation first. If every per-key payload matcher is literal false, it omits
the per-key matcher `CASE` and its arguments. It retains Build-time key
UTF-8/NUL validation, pair-null checks, outer-list checks, source evaluation,
and empty-list behavior. Mixed, `Any`, and data-dependent matchers remain on
their existing path. Identical but nonconstant matchers, including nullable
String null tests, are not collapsed.

For a guard on a produced JSON value, the ordinary normalizer still runs
fully in an isolated validation pass. The original carrier is reused only
when the original producer proof establishes that it is already a compatible
fixed point of normalization. Reuse retains the original presence mask and
producer proof; it never relabels the carrier with the narrower guard target.
Actual conversions and values without a compatible proof keep ordinary
normalization. Raw JSON/JSONB does not gain a proof from static schema
inspection alone. JSONB can qualify after metadata restoration and a typed
map/filter produces a compatible JSON carrier.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| Recovered item-pair payload matcher proven literal false | Applied | Only the matcher `CASE` and its unused arguments are omitted. |
| Mixed, `Any`, or data-dependent item matcher | Not applied | Payload dispatch still determines the result. |
| Guard on a produced JSON carrier with compatible original proof | Applied | Full ordinary normalization is validated before the original carrier is reused. |
| Raw or converted JSON/JSONB without a compatible producer proof | Not applied | Normalization and required metadata restoration remain in force. |
| Local backend | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [d61d3e2](https://github.com/ornew/hff/commit/d61d3e292210c5ea50eb5cc9b5267dca2ef04252). The candidate observations were collected from a working-tree implementation based on that snapshot; no candidate commit existed when the SQL was collected. Save the following public-API generator as `/private/tmp/hff-guard-normalization-audit.go` and run it from the baseline and candidate checkouts with Go 1.27.1, changing `before` to `after` in the output names:

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
	typ := schema.Struct(
		schema.Field("items", schema.NullableType(
			schema.ListOf(schema.NullableType(schema.FloatType)),
		)),
		schema.Field("obj", schema.NullableType(schema.Struct(
			schema.Float("f"), schema.Int("i"),
		))),
	)
	e, err := engine.New(
		engine.WithSchema(typ),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}

	sources := []string{
		`items:map(x=>x):[list[float|null]]:json="[]"`,
		`items:filter(x=>x!=null):[list[float|null]]:json="[]"`,
		`items:map(x=>x):[list[number|null]]:json="[]"`,
		`items:map(x=>x):[list[float|null]]:[list[number|null]]:json="[]"`,
		`items:map(x=>x):[list[float]]:json="[]"`,
		`items:[list[float|null]]:json="[]"`,
		`items:map(x=>obj):[list[map[number]]]:json="[]"`,
		`items:map(x=>x):[list[float|null]|null]:json="[]"`,
		`items:map(x=>x):[list]:json="[]"`,
	}
	var out []map[string]any
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		var itemOptions, objectOptions []hffsql.JSONBindingOption
		if format == hffsql.JSONB {
			itemOptions = append(itemOptions,
				hffsql.WithTypeMetadata(hffsql.Column("records", "items_meta")))
			objectOptions = append(objectOptions,
				hffsql.WithTypeMetadata(hffsql.Column("records", "obj_meta")))
		}
		bindings := []hffsql.Binding{
			hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, itemOptions...),
			hffsql.BindJSON(hff.Path{"obj"}, hffsql.Column("records", "obj"), format, objectOptions...),
		}
		for _, source := range sources {
			program, err := e.Compile(context.Background(), source)
			if err != nil {
				panic(err)
			}
			query, err := hffsql.Build(context.Background(), program,
				hffsql.WithDialect(hffsql.PostgreSQL),
				hffsql.WithBindings(bindings...),
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

Run, for example, from the baseline checkout:

```sh
go run /private/tmp/hff-guard-normalization-audit.go \
  > /private/tmp/hff-guard-canonical-guard-normalization-before.json \
  2> /private/tmp/hff-guard-canonical-guard-normalization-before.log
```

The item-payload matcher measurements use the same public-API generator and
queries documented in [Optimization 056](056-postgresql-item-any-guards.md),
with result files named `/private/tmp/hff-guard-canonical-item-any-guard-before`
and `-after` (each with `.json` and `.log` files).

## Results

The observations report generated `WHERE` size and argument count. They are
SQL-shape measurements, not execution-time or runtime-performance results.

| Workload | JSON before → after | JSONB before → after |
|:--|:--|:--|
| Typed map, list guard, `json()` | 8,493 → 4,363 B | 10,291 → 6,106 B |
| Typed filter, list guard, `json()` | 9,885 → 5,795 B | 11,604 → 7,514 B |
| Widened list guard | 8,556 → 4,381 B | 10,354 → 6,124 B |
| Chained list guards | 13,820 → 9,690 B | 15,618 → 11,433 B |
| Non-null list guard | 7,989 → 4,219 B | 9,773 → 5,958 B |
| Raw-input control | 7,353 → 7,353 B | 9,105 → 9,105 B |
| Struct-to-Map guard | 14,532 → 7,434 B; 9 → 7 args | 18,010 → 10,882 B; 9 → 7 args |
| Nullable union | 8,941 → 4,491 B | 10,753 → 6,238 B |
| Unconstrained list guard control | 3,684 → 3,684 B | 5,414 → 5,414 B |

For the separate item-payload matcher case, the impossible pair guard
`labels:items:any(p=>p:[list[string]]:exists)` shrank from 13,587 to 11,803 B
for JSON and 13,616 to 11,832 B for JSONB. Its argument count fell from 24 to
12 in both formats. Ten other controls retained their SQL and arguments.
Pair/list checks remain in the generated predicate. The schema has twelve
optional, nullable Bytes fields, `label00` through `label11`, under an
optional, nullable `labels` Struct. The public-API generator in Optimization
056 includes this exact query and schema, plus the other control queries.

The related PostgreSQL checks covered the normalization and item-guard
matrices across JSON and JSONB, including 7,588 outputs for the impossible-
matcher matrix. They passed with the existing normalized-map/fold and Float
decode-failure regressions. These are selected parity checks, not a claim of
full backend conformance.

Plain `EXPLAIN (FORMAT JSON, COSTS OFF)` on PostgreSQL 18.6, with JIT disabled
and a 30-second statement timeout, used
`records(items, obj, items_meta, obj_meta)` with values
`('[1.0,null,2.5]'::json, NULL::json, NULL::jsonb, NULL::jsonb)` for the first
two JSON queries. Their `Function Scan` occurrences went from 10 to 4 and 11
to 5, respectively; `Aggregate` occurrences went from 4 to 3 for each. The
third query used `records(labels)` with `'{"label00":"YQ=="}'::json`; its
impossible item-guard plan retained three `Function Scan` and two `Aggregate`
occurrences. Plans used the arguments emitted by the public-API generator.
These are planner-shape observations only; no execution counts or runtime
gains are inferred. No `EXPLAIN ANALYZE` was run.

## Correctness and tradeoffs

The fixed-point path does not skip source evaluation, Build validation, guard
matching, or required Float and metadata handling. It reuses a JSON value only
when the original producer proof matches the normalized representation
required by the guard. If that proof fails, the ordinary normalization path
remains. The literal-false rewrite likewise keeps Build-time key validation
and pair/list checks. Identical but nonconstant matcher expressions are
outside this rewrite and retain their current dispatch.

## Decision and limits

Apply both rewrites only when their respective proofs hold; retain fallback
lowering otherwise. The SQL-size observations do not establish runtime
performance. Other guard and normalization canonicalization opportunities
remain to be audited.
