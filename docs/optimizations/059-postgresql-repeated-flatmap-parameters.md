# 059. Share repeated PostgreSQL flatmap parameter rows

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

A `flatmap()` callback can construct a list with repeated copies of one input
parameter. Lowering each copy independently repeats the same row expansion and
callback dependencies in generated SQL. For a bounded set of expressions, the
PostgreSQL builder can validate the original constructor and then reuse one
ordered row source for all copies.

The proof applies to a nonempty literal list or strict built-in `concat()`
tree when every leaf is the same bare lexical parameter, identified by its
binding identity. The builder does not compare SQL text or infer equivalence
from types. It retains the first value descriptor and uses an ordinal source
for repeated rows. Source order is preserved; ordinary null is repeated data,
and a no-value parameter still fails the list. Constructor validation occurs
before the shared source is selected.

## Applicability

| Callback list expression | Status | Reason |
|:--|:--|:--|
| `[v,v]` for one bare parameter `v` | Applied | Both leaves share one lexical binding. |
| A strict built-in concat tree whose leaves are all `v` | Applied | The same parameter identity is reused across the tree. |
| `[v[1],v[1]]` or `[v!,v!]` | Not applied | The leaves are expressions, not bare parameter references. |
| Equal-looking expressions from distinct bindings | Not applied | SQL-text or descriptor equality is not a binding-identity proof. |
| Local backend | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [a8c9a6b](https://github.com/ornew/hff/commit/a8c9a6b474a909d8b8bad797083a3929a7893ecb).
The candidate implementation accompanying this record is based on that
revision. The public API generator uses Go 1.27.1 and emits
parameterized `WHERE` SQL and ordered arguments for JSON and JSONB; it does
not execute queries or measure runtime performance.

Save this program as `/private/tmp/hff-repeated-flatmap-audit.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ornew/hff"
	sql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(
			schema.Map("labels", schema.NullableType(schema.BytesType)),
		)))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}

	sources := []string{
		`labels:items:map(p=>p[1]):flatmap(v=>[v,v]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1],p[1]]):map(v=>v:type):json:exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v]:concat([v])):map(v=>v:type):json:exists`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v,v]):flatmap(v=>[v,v]):map(v=>v:type):json:exists`,
	}
	var results []map[string]any
	for _, format := range []sql.JSONFormat{sql.JSON, sql.JSONB} {
		for _, source := range sources {
			program, err := e.Compile(context.Background(), source)
			if err != nil {
				panic(err)
			}
			query, err := sql.Build(context.Background(), program,
				sql.WithDialect(sql.PostgreSQL),
				sql.WithBindings(sql.BindJSON(hff.Path{"labels"}, sql.Column("records", "labels"), format)),
			)
			if err != nil {
				panic(err)
			}
			results = append(results, map[string]any{
				"format": format,
				"source": source,
				"where":  query.Where(),
				"args":   query.Args(),
			})
			fmt.Fprintf(os.Stderr, "%s %dB %dargs %dunion %darray-sites %s\n",
				format, len(query.Where()), len(query.Args()),
				strings.Count(query.Where(), "UNION ALL"),
				strings.Count(query.Where(), "json_array_elements(")+strings.Count(query.Where(), "jsonb_array_elements("),
				source,
			)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		panic(err)
	}
}
```

Run it from the baseline and candidate checkouts:

```sh
# Baseline checkout.
GOTOOLCHAIN=go1.27.1 go run /private/tmp/hff-repeated-flatmap-audit.go \
  > /private/tmp/hff-repeated-flatmap-before.json \
  2> /private/tmp/hff-repeated-flatmap-before.log

# Candidate checkout.
GOTOOLCHAIN=go1.27.1 go run /private/tmp/hff-repeated-flatmap-audit.go \
  > /private/tmp/hff-repeated-flatmap-after.json \
  2> /private/tmp/hff-repeated-flatmap-after.log
```

The SQL-size observations use the same generator and fixtures at the baseline
and candidate. Raw output is retained at
`/private/tmp/hff-repeated-flatmap-before.json` and `.log`, and the matching
`after.json` and `.log` files.

## Results

`WHERE` byte counts are generated SQL size, not execution time:

| JSON workload | JSON baseline → candidate | JSONB baseline → candidate |
|:--|--:|--:|
| `flatmap(v=>[v,v])` | 7,552 → 5,820 | 7,558 → 5,826 |
| `flatmap(v=>[v]:concat([v]))` | 7,578 → 5,820 | 7,584 → 5,826 |
| Two nested repeated flatmaps | 13,139 → 6,962 | 13,145 → 6,968 |
| Repeated `p[1]` expression control | 8,069 → 8,069 | 8,075 → 8,075 |

Plain `EXPLAIN` (without `ANALYZE`) ran on PostgreSQL 18.6 with
`extra_float_digits=1`, JIT disabled, and a 30-second statement timeout. The
fixture was a `records(labels)` JSON row containing `{"app":"YQ==","z":null}`
for both revisions. The plans for `[v,v]`, `[v]:concat([v])`, and the two
nested repeated flatmaps showed these plan-node counts, baseline → candidate:

| Workload | Function Scan | Append | Values Scan | Materialize | Aggregate |
|:--|--:|--:|--:|--:|--:|
| `[v,v]` | 8 → 6 | 2 → 0 | 0 → 2 | 0 → 2 | 5 → 5 |
| `[v]:concat([v])` | 8 → 6 | 2 → 0 | 0 → 2 | 0 → 2 | 5 → 5 |
| Two nested repeated flatmaps | 14 → 7 | 3 → 0 | 0 → 5 | 0 → 5 | 5 → 5 |

These plans still contain array scans and other work; the node counts do not
establish execution counts or runtime improvement. The repeated `p[1]`
expression control was not part of the plain-plan comparison.

## Correctness and tradeoffs

The final selected PostgreSQL checks passed 178,618 logged query/record
results across 380 groups, including 33,532 results for 19 typed fixtures in
JSON and JSONB. Separate assertions passed 64 exact-output cases for
multiplicity, order, indexing, folds, `find()`, `distinct()`, null, and
no-value, plus 16 raw-Float boundary cases. Build tests retained 136 existing
rejections in the candidate; the same rejections reproduced on the baseline
with sharing disabled. Root unit, vet, integration vet, SQL
race, and WASM checks passed. Logs are retained at
`/private/tmp/hff-repeat-corrected-pg.log` and
`/private/tmp/hff-repeat-final-checks.log`; this selected matrix is not a claim
of full backend conformance.

The optimization is limited to copies of the same unchanged callback
parameter. Indexed, guarded, method, and other computed expressions retain
their existing lowering, even when they look equivalent. Full constructor
validation and argument/cancellation behavior remain required. The repeated
row source preserves list order and null elements; no-value remains a list
failure. Performance evidence is limited to SQL shape; no benchmark or
`EXPLAIN ANALYZE` result was collected.

## Decision and limits

Use the shared row source only for the lexical-identity proof above. This does
not cover duplicated slot expressions such as `[p[1],p[1]]` or computed
branches. Equal-looking values from distinct lexical bindings do not share
the proof; a captured parameter qualifies only when every leaf refers to that
same binding. General branch sharing across arbitrary transformations retains
the ordinary path unless it independently meets the proof.
