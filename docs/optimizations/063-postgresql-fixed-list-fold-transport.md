# 063. Preserve fixed-list item positions in PostgreSQL folds

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-16

## Summary

For a bounded deferred fold over lists constructed from item values, preserve
the statically known list-arm positions so a callback can transport only the
complete selected child value. Direct finite integral numeric literal indexes
qualify, including negative indexes and integral Float literals. Other index
and whole-list observations retain ordinary lowering.

## Problem and mechanism

A deferred item callback can construct a fixed list containing repeated
schema-known values, then fold over those lists while reading one arm. Encoding
and expanding the full tagged list at every fold step repeats work for arms
the callback does not observe. The bounded path proves each selected arm from
the literal list positions and its lexical item source, then uses a compact
private JSON array and typed decoder for the selected values. Repeated original
arms are encoded once. The selected complete child retains its declared type
and Float-loss metadata; this does not change the public binding format.

Eligibility requires a statically fixed list constructor and direct finite
integral numeric literal indexes on the fold item's lexical binding. Negative
indexes and integral Float literals resolve to their corresponding positions.
Whole-list reads or guards, bare item use, dynamic or fractional indexes, and
custom translators anywhere in the source, seed, or callback body retain
ordinary lowering. Native and safe identity-fold paths remain preferred.

The original source, seed, and callback are validated with nested pruning
disabled before the retained plan is adopted. Source order, null/no-value
state, fold order, and first-failure behavior remain unchanged. This private
transport does not add a public JSON or binding representation.

## Applicability

| Fold callback | Behavior |
|:--|:--|
| `(a,v)=>a+v[0]:string:len` over `[p[1],p[1]]` | Select the first literal arm and retain its source type. |
| `(v,a)=>a+v[-1]:string:len` over `[p[1],"middle",p[1]]` | Resolve the negative literal index against the fixed list length. |
| Direct integral Float literal index such as `v[1.0]` | Apply the same fixed-position proof as an integral Int literal. |
| `(a,v)=>a+v:size` or a guard on `v` | Keep ordinary lowering because the whole list is observed. |
| Dynamic or fractional index, or custom translator in source/seed/body | Keep ordinary lowering. |

## SQL-shape observations

The comparison baseline is [a448729](https://github.com/ornew/hff/commit/a4487292684715e371b6cebbb8a6077c2a778c37). Generated `WHERE` sizes were recorded through the public API for JSON and JSONB. These measurements describe SQL shape, not runtime performance.

| JSON workload | Baseline → implementation bytes | Array constructor calls | Arguments |
|:--|--:|--:|--:|
| First selected arm from a repeated pair | 11,493 → 8,239 | 8 → 7 | 8 → 7 |
| Last selected arm in a right fold | 13,245 → 8,374 | 9 → 8 | 9 → 8 |
| Nested list and object with selected list arm | 10,642 → 7,373 | 7 → 6 | 7 → 6 |
| Nested captured fold callback | 12,718 → 9,464 | 9 → 8 | 9 → 8 |

JSONB results were 9 bytes larger in the eight eligible cases. Four fallback
controls per format retained identical SQL and arguments. The expanded
PostgreSQL matrix passed 16,360 query/record results across 40 fixture groups
for 26 source forms; six strict Build rejections remain separately asserted.
Another 176 typed/inferred expected outputs passed. Related object-fold and
unused-item-fold suites contributed 11,376 and 13,904 additional results,
respectively; these are not fixed-list results.

Plain `EXPLAIN` on PostgreSQL 18.6, with JIT disabled and a 30-second timeout,
showed Function Scan counts changing 7→4, 10→4, 11→5, and 8→5, and Aggregate
counts changing 4→3, 4→3, 7→5, and 4→3 for the four eligible workloads. Sort
counts changed 5→3, 5→3, 7→4, and 6→4. Recursive Union counts were unchanged
(1, 1, 1, and 2). These are plan-shape observations only; no `EXPLAIN
ANALYZE` or runtime measurements were collected.

## Reproduction

This public API program builds eight source expressions for JSON and JSONB,
then prints each predicate, argument list, and selected SQL-shape counts. It
does not execute a database query. It emits 16 SQL/argument outputs: eight
expressions across the two formats.

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/ornew/hff"
	s "github.com/ornew/hff/backends/sql"
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
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v[0]:string:len)>5`,
		`labels:items:map(p=>[p[1],"middle",p[1]]):foldr(0,(v,a)=>a+v[-1]:string:len)>5`,
		`labels:items:map(p=>[[p[1],p[1]],{"unused":[p[1],p[1]]}]):foldl(0,(a,v)=>a+v[0]:size)>2`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v[0]:string:len))>5`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v:size)>5`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v![0]:string:len)>5`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(0,(a,v)=>a+v[v:size-2]:string:len)>5`,
		`labels:items:map(p=>[p[1],p[1]]):foldl(true,(a,v)=>a v[0.5]=null)`,
	}

	var rows []map[string]any
	for _, format := range []s.JSONFormat{s.JSON, s.JSONB} {
		for _, source := range sources {
			program, err := e.Compile(context.Background(), source)
			if err != nil {
				panic(err)
			}
			query, err := s.Build(
				context.Background(),
				program,
				s.WithDialect(s.PostgreSQL),
				s.WithBindings(
					s.BindJSON(hff.Path{"labels"}, s.Column("records", "labels"), format),
					s.Bind(hff.Path{"offset"}, s.Column("records", "offset")),
				),
			)
			if err != nil {
				panic(err)
			}
			rows = append(rows, map[string]any{
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
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
```

Save the program as `/tmp/fold-audit.go` outside the HFF checkout and run
`GOTOOLCHAIN=go1.27.1 go run /tmp/fold-audit.go` from the repository root.

## Correctness and tradeoffs

The optimization retains the complete selected child subtree, including
nested values and required numeric provenance. It does not discard source
evaluation or callback validation, and it does not alter ordering or presence
semantics. Unsupported observations continue through the ordinary lowering
path rather than being inferred from SQL text or result type alone.

## Decision and limits

The implementation applies only when literal list positions and the selected
lexical item source establish the transport proof. Bare/root-guard reads,
dynamic and fractional indexes, whole-list observations, custom translators,
and unrelated fold forms remain outside the measured scope. Focused PostgreSQL
tests used:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^(TestFixedDeferredFoldListsBuild|TestFixedDeferredFoldList|TestFixedFoldList)' \
  -count=1

: "${HFF_POSTGRES_CONTAINER:?set to a running PostgreSQL container name or ID}"
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLFixedFold(ListsMatchLocal|ListExactOutputs)$' \
  -count=1
```

Root unit, root vet, integration vet, full SQL race, and WASM checks passed.
The focused PostgreSQL matrix and plain-plan observations above also passed.
SQL-size and plan-shape observations do not establish lower execution time;
benchmark and `EXPLAIN ANALYZE` measurements remain deferred.
