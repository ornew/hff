# 061. Omit unused item payloads from PostgreSQL deferred folds

- **Status**: Implemented
- **Date**: 2026-10-16

## Summary

For a bounded class of PostgreSQL JSON/JSONB folds, the lowering omits
the deferred item payload when the fold callback never reads its item binding.
It preserves the source rows, fold order, seed, callback result, null/presence,
and first-failure behavior. This is a SQL-shape optimization; it does not turn
the fold into a count or remove the accumulator.

## Problem and mechanism

Some callback pipelines produce a typed packet for each input row. A fold may
then use only the accumulator or a separate captured value, leaving the packet
payload unused. Carrying and decoding that payload through each recursive step
adds SQL without affecting the callback result.

The candidate applies only after the existing native and safe identity-fold
paths have had priority. It proves that the fold item's lexical binding is
unused throughout the callback body, including nested callbacks. A capture of
another binding is allowed; a nested callback that captures the fold item makes
it used. If any custom extension signature occurs in the fold, this candidate
path is not used.

The original source, seed, and callback are fully validated before the
optimized packet path is selected. The added validation probe reuses the
prepared source plan; source rows, filters, and order remain unchanged. The
fold still processes every source row and preserves ordinary null, presence,
no-value, seed behavior, and recursive first failure. Only the unused item
payload and its decoding are omitted from the packet; accumulator state is
unchanged. No native projection or size/count rewrite is attempted.

## Applicability

| Fold callback | Status | Reason |
|:--|:--|:--|
| `(a,v)=>a+1` | Candidate | The item binding is unused throughout the body. |
| `(a,v)=>[1,2]:foldl(a,(b,v)=>b+v)` | Candidate | The nested `v` shadows the outer item binding; the outer item is not captured. |
| `(a,v)=>a+offset` | Candidate | A capture of a different binding does not observe the item. |
| `(a,v)=>a+v:size` | Not applied | The callback reads the item. |
| `(a,v)=>[1]:foldl(a,(b,x)=>b+v:size)` | Not applied | The nested callback captures the item binding. |
| A custom extension signature in the source, seed, or callback body | Not applied | Extension validation is not duplicated in this candidate path. |
| Existing native or safe identity-fold case | Earlier path | This packet-fold optimization does not replace the established lowering. |

## Reproduction

The SQL-shape baseline is [f82b408](https://github.com/ornew/hff/commit/f82b408dc111644e97fe9bc2b448eba4adf0aa30). The implementation is based on that revision. The public API generator below builds parameterized PostgreSQL `WHERE` expressions for JSON and JSONB and writes each query and argument list; it does not execute queries.

Save as `/private/tmp/hff-unused-fold-audit.go`:

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
		engine.WithSchema(schema.Struct(
			schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(
				schema.Optional(schema.Nullable(schema.Bytes("asset"))),
				schema.Optional(schema.Nullable(schema.String("name"))),
			)))),
			schema.Int("offset"),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}

	sources := []string{
		`labels:items:map(p=>p[1]):foldl(0,(a,v)=>a+1)=labels:size`,
		`labels:items:map(p=>p[1]):foldr(0,(v,a)=>a+1)=labels:size`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+1)=labels:size`,
		`labels:items:map(p=>{"unused":p[1],"nested":[p[1],p[1]]}):foldl(0,(a,v)=>a+1)=labels:size`,
		`labels:items:map(p=>[p[1],p[1]]):foldr(0,(v,a)=>a+1)=labels:size`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>[1,2]:foldl(a,(b,v)=>b+v))=labels:size*3`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v:size)>0`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v:size))>0`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+offset)=labels:size*offset`,
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
				sql.WithBindings(
					sql.BindJSON(hff.Path{"labels"}, sql.Column("records", "labels"), format),
					sql.Bind(hff.Path{"offset"}, sql.Column("records", "offset")),
				),
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
			fmt.Fprintf(os.Stderr, "%s %dB %dargs %darray %drecursive %s\n",
				format, len(query.Where()), len(query.Args()),
				strings.Count(query.Where(), "json_build_array("),
				strings.Count(query.Where(), "WITH RECURSIVE"), source,
			)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(results); err != nil {
		panic(err)
	}
}
```

Run it from the baseline and candidate checkouts with Go 1.27.1:

```sh
GOTOOLCHAIN=go1.27.1 go run /private/tmp/hff-unused-fold-audit.go \
  > /private/tmp/hff-unused-fold-before.json \
  2> /private/tmp/hff-unused-fold-before.log

GOTOOLCHAIN=go1.27.1 go run /private/tmp/hff-unused-fold-audit.go \
  > /private/tmp/hff-unused-fold-after.json \
  2> /private/tmp/hff-unused-fold-after.log
```

## Results

The table reports generated `WHERE` bytes and `json_build_array` call sites,
baseline to candidate. These call-site counts describe SQL shape, not array
expansions, runtime, allocations, or database work.

| Fold workload | JSON bytes / `json_build_array` calls | JSONB bytes / `json_build_array` calls |
|:--|--:|--:|
| Direct `map()` item, `foldl()` | 5,566 / 2 → 5,512 / 1 | 5,576 / 2 → 5,522 / 1 |
| Direct `map()` item, `foldr()` | 5,567 / 2 → 5,513 / 1 | 5,577 / 2 → 5,523 / 1 |
| Repeated-list item, `foldl()` | 5,810 / 3 → 5,512 / 1 | 5,820 / 3 → 5,522 / 1 |
| Nested object/list item, `foldl()` | 6,417 / 4 → 6,022 / 1 | 6,427 / 4 → 6,032 / 1 |
| Repeated-list item, `foldr()` | 5,811 / 3 → 5,513 / 1 | 5,821 / 3 → 5,523 / 1 |
| Nested shadowed fold, `foldl()` | 7,594 / 4 → 7,296 / 2 | 7,604 / 4 → 7,306 / 2 |
| Item used by `size()` | 6,171 / 3 → 6,171 / 3 | 6,180 / 3 → 6,180 / 3 |
| Nested callback captures item | 7,396 / 4 → 7,396 / 4 | 7,405 / 4 → 7,405 / 4 |
| Capture of other binding, item unused | 6,122 / 3 → 5,824 / 1 | 6,132 / 3 → 5,834 / 1 |

The related PostgreSQL selection passed 183,566 logged query/record results
across 547 groups, including 13,904 new typed-parameter results across 40
groups. A separate set of 72 independent expected-value checks also passed.
These are selected parity results, not a claim of full backend conformance.

Nine public sources were prepared and executed with omitted type OIDs for both
JSON formats on both baseline and candidate, for 36 positive cases; all passed.
Typed-parameter parity includes a separate nullable-source fold case whose
`Query.Args` contains an unused bound argument. PostgreSQL rejects its untyped
`PREPARE` on both baseline and candidate because the SQL has no matching
placeholder; this negative result is not included in the logged parity total
or the 36 positive omitted-OID cases.

Plain `EXPLAIN` used PostgreSQL 18.6 with JIT disabled and a 30-second
statement timeout, against `records(labels, offset)` rows containing a JSON
object with `asset: "YQ=="` and `name: "Alice"`, plus `offset = 2`. For the
direct item workload, Function Scan, Aggregate, Recursive Union, and WorkTable
counts were unchanged at 5, 4, 1, and 1. The repeated-list, nested-object, and
nested-shadowed-fold workloads each removed one Aggregate node; the shadowed
fold retained its two Recursive Union and WorkTable nodes. These plan-shape
observations do not establish runtime improvements.

Root unit, vet, integration-vet, SQL race, and WASM checks passed. Raw generator
output is retained at `/private/tmp/hff-unused-fold-before.json`,
`/private/tmp/hff-unused-fold-after.json`,
`/private/tmp/hff-unused-fold-before.log`, and
`/private/tmp/hff-unused-fold-after.log`; validation logs are at
`/private/tmp/hff-unused-fold-final-pg.log` and
`/private/tmp/hff-unused-fold-final-checks.log`.

## Limitations

This optimization applies only to packet-based PostgreSQL folds that pass the
binding-use proof and contain no custom extension signature in the source,
seed, or callback body. It does not produce native typed projections, prune
observed fields or accumulator state, or rewrite the fold as
`size()`/`count()`. Input-using folds, nested item captures, extensions, and
other uncertain sources retain their ordinary path. No runtime-performance
claim is made.
