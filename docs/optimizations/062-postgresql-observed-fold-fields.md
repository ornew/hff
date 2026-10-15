# 062. Omit unobserved fields from PostgreSQL deferred fold packets

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-16

## Summary

For a bounded PostgreSQL JSON/JSONB fold over constructed-object items, retain
only the complete object fields the callback reads. Direct field access and
literal-string indexing qualify; whole-item reads, whole/root-item guards,
dynamic indexes, and otherwise unproven callbacks retain ordinary packet
lowering.

## Problem and mechanism

A deferred fold can receive an object whose fields contain recovered tagged
values. If the callback reads only one field, serializing and decoding the
entire object also processes unused fields and nested values. This implementation
projects observed fields while retaining each selected child subtree and its
declared type.

The path applies after native and safe identity-fold paths. Eligibility is
based on the fold item's lexical binding and direct field or literal-string
index observations. It preserves original field order and selected child
values. Original object fields remain represented in their original names,
positions, and types; unobserved fields use dummy transport entries rather than
being removed from the semantic object. Selected children retain complete
`bytes` and `string` values and any Float-loss metadata. It does not change the object
or binding format, infer tags for ambiguous stored values, or project through
dynamic indexes or guards on the whole/root item.

The original source, seed, and callback are fully validated with nested
pruning disabled in the validation pass. The retained source plan preserves
row order, presence, null/no-value state, and recursive first failure. Any
custom translator signature in the source, seed, or callback body excludes the
optimized path. Whole-item reads and non-object root carriers use the ordinary
packet path.

## Applicability

| Fold callback | Behavior |
|:--|:--|
| `(a,v)=>a+v.used:string:len` | Retain the complete `used` child subtree. |
| `(a,v)=>a+v["used"]:string:len` | Retain the literal-key child subtree. |
| `(a,v)=>a+v.used!:string:len` | Retain the selected child and apply its guard. |
| `(a,v)=>a+v.used:size` where `used` is a list | Retain the complete nested list. |
| A nested callback captures `v.used` | Retain the captured selected subtree and preserve lexical scope. |
| `(a,v)=>a+v:size` | Keep the whole-item packet path. |
| `(a,v)=>a+v[labels.name]:string:len` | Keep the dynamic-index packet path. |
| A guard on the whole/root `v`, or a custom translator in source, seed, or body | Keep ordinary lowering. |

## SQL-shape observations

The comparison baseline is [f2bd8bc](https://github.com/ornew/hff/commit/f2bd8bc38655167beac0fd416b117d6fb73b3fa3). Generated `WHERE` sizes and `json_build_array` call sites were observed with the public API generator below; these figures describe SQL shape, not runtime performance.

| JSON workload | Baseline → implementation bytes | Array-constructor call sites |
|:--|--:|--:|
| Direct field read | 8,394 → 8,096 | 4 → 2 |
| Unobserved fields in `foldr()` | 9,002 → 8,607 | 5 → 2 |
| Selected nested list | 7,644 → 7,249 | 6 → 3 |
| Nested callback captures selected field | 9,619 → 9,321 | 5 → 3 |

JSONB produced comparable reductions, generally 9 bytes larger. Three
fallback-control queries retained identical SQL and arguments, and all 14
argument lists in the audit were unchanged. The related PostgreSQL selection
passed 25,280 logged query/record results across 80 groups, including 11,376
new observed-field results and 13,904 unused-item-fold results. A separate
128 typed/inferred expectation set passed, as did 72 prior exact-output checks,
four preparation cases, and 1,968 null-argument parity results.

Plain `EXPLAIN` on PostgreSQL 18.6 used JIT disabled, a 30-second timeout, and
the fixed `asset: "YQ=="`, `name: "Alice"` fixture. Across the four workloads
above, Aggregate nodes changed 4→3, 4→3, 6→5, and 4→3; Sort nodes changed
4→3, 4→3, 5→4, and 5→4. Function Scan and Recursive Union counts did not
change. These are planner-shape observations only; no execution timing or
`EXPLAIN ANALYZE` was collected.

Focused verification used:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^TestDeferredFoldObservedObjectFieldsBuild$' -count=1

: "${HFF_POSTGRES_CONTAINER:?set to a running PostgreSQL container name or ID}"
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLDeferredFold(ObjectFieldExactOutputs|ObservedFieldsMatchLocal)$' \
  -count=1
```

Root unit, vet, integration vet, focused SQL race, and WASM checks also passed. No
runtime-speed claim follows from the SQL or plan observations.

## Reproduction

The following public API program builds JSON and JSONB predicates and prints
their SQL and arguments. It does not execute a query.

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
		`labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.used:string:len)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":{"a":p[1],"b":[p[1],p[1]]}}):foldr(0,(v,a)=>a+v["used"]:string:len)>5`,
		`labels:items:map(p=>{"used":[p[1],p[1]],"unused":{"a":p[1],"b":[p[1],p[1]]}}):foldl(0,(a,v)=>a+v.used:size)>2`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>[1]:foldl(a,(b,x)=>b+v.used:string:len))>5`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v:size)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v!.used:string:len)>5`,
		`labels:items:map(p=>{"used":p[1],"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v[labels.name]:string:len)>5`,
	}

	var rows []map[string]any
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
			rows = append(rows, map[string]any{
				"format": format,
				"source": source,
				"where":  query.Where(),
				"args":   query.Args(),
			})
			fmt.Fprintf(os.Stderr, "%s %dB %dargs %darray %s\n",
				format, len(query.Where()), len(query.Args()),
				strings.Count(query.Where(), "json_build_array("), source,
			)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
```

Run the same generator from the baseline and candidate checkouts with Go
1.27.1, then compare the emitted SQL and argument lists. No benchmark or
`EXPLAIN ANALYZE` was run for these observations.
