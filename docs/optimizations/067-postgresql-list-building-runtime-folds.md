# 067. Lower list-building PostgreSQL folds without growing recursive state

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-16

## Summary

A bounded PostgreSQL deferred-fold path builds a list-valued accumulator with
ordered aggregation when the ordinary JSON fold probe declines and the callback
only appends or prepends a nonempty list literal. The accumulator seed must be
the literal empty list, and no list element may depend on the accumulator.

## Problem and mechanism

A recursive JSON fold serializes the accumulator on each iteration. For a
list-building callback, that repeatedly reconstructs a list even when each
iteration adds an independently computed literal chunk. This lowering keeps
the prepared source plan and replaces the growing recursive accumulator with
one ordered aggregate.

The recognized callback has one strict built-in list `concat()` operation
between the bare accumulator parameter and a nonempty list literal. The
accumulator binding must not be referenced anywhere else in the callback, even
inside nested callbacks, and every expression in the literal chunk must be
independent of that binding. The seed is exactly `[]`. This path applies only
after ordinary JSON fold lowering declines; folds already handled by existing
lowerings remain unchanged.

The four supported directions preserve ordinary fold results:

| Fold direction | Callback result | Chunk order |
|:--|:--|:--|
| `foldl()` | `acc:concat([value])` | Input order |
| `foldr()` | `[value]:concat(acc)` | Input order |
| `foldl()` | `[value]:concat(acc)` | Reverse input order |
| `foldr()` | `acc:concat([value])` | Reverse input order |

Every source item emits a nonempty chunk. Literal element order is preserved
within each chunk. Null elements remain list data; a Boolean presence state
keeps no-value distinct from null. Source null, missing, and no-value retain
their existing precedence. Source evaluation, captures unrelated to the
accumulator, fold result roles, and Float-loss provenance remain unchanged.

The source has already been prepared by the time this path is considered.
The complete old runtime callback is validated in an isolated builder with
nested list-building specialization disabled; the prepared source plan and
seed are retained. This does not rewrite or re-evaluate the AST. Custom
translators anywhere in the source, seed, or callback exclude the path. A
computed, nullable, or nonempty seed; an empty or computed callback chunk;
another accumulator use; or a callback without this exact built-in shape keeps
the existing path. Other fold consumers and callback semantics are unchanged.

## Applicability

| Fold input/callback | Behavior | Reason |
|:--|:--|:--|
| `foldl([], (acc,item)=>acc:concat([item]))` | Applied | Appends each nonempty chunk in input order. |
| `foldr([], (item,acc)=>[item]:concat(acc))` | Applied | Prepends chunks while visiting in reverse, preserving input order. |
| `foldl([], (acc,item)=>[item]:concat(acc))` | Applied | Prepends chunks, reversing chunk order. |
| `foldr([], (item,acc)=>acc:concat([item]))` | Applied | Appends chunks while visiting in reverse, reversing chunk order. |
| A multi-element nonempty literal chunk with accumulator-independent elements | Applied | The chunk is emitted in its original element order. |
| `foldl(seed, ...)` where the seed is nonempty, computed, or nullable | Not applied | The optimized empty-seed state proof does not apply. |
| The callback uses the accumulator in an element expression, capture, or another operation | Not applied | The added chunk would depend on accumulated state. |
| A custom translator occurs in the source, seed, or callback | Not applied | Full validation and the generic lowering path remain required. |
| Ordinary JSON fold lowering succeeds | Not applied | The existing path retains priority. |

## Reproduction

The baseline is
[28b5b04](https://github.com/ornew/hff/commit/28b5b04393e4d27c7039a6ee1c6e380eba654eac).
The public API
generator below emits SQL and ordered arguments for eight HFF expressions with
JSON and JSONB bindings. It does not execute queries or measure runtime
performance.

Save the program as `/tmp/building-fold-audit.go` outside the checkout and
run `GOTOOLCHAIN=go1.27.1 go run /tmp/building-fold-audit.go` from the HFF
repository root at both revisions.

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

	values := `labels:items:map(p=>p[1])`
	sources := []string{
		values + `:foldl([],(a,v)=>a:concat([v:type])):json:exists`,
		values + `:foldr([],(v,a)=>[v:type]:concat(a)):json:exists`,
		values + `:foldl([],(a,v)=>[v:type]:concat(a)):json:exists`,
		values + `:foldr([],(v,a)=>a:concat([v:type])):json:exists`,
		values + `:foldl([],(a,v)=>a:concat([v])):map(v=>v:type):json:exists`,
		values + `:foldr(["YQ==":frombase64],(v,a)=>[v]:concat(a)):map(v=>v:type):json:exists`,
		values + `:foldl(["start"],(a,v)=>a:concat([v:type,v:exists])):json:exists`,
		values + `:foldl([],(a,v)=>a:concat([v!])):json:exists`,
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
				fmt.Fprintf(os.Stderr, "REJECT %s %s %v\n", format, source, err)
				continue
			}
			rows = append(rows, map[string]any{
				"format": format,
				"source": source,
				"where":  query.Where(),
				"args":   query.Args(),
			})
			fmt.Fprintf(os.Stderr, "%s %dB %dargs %drecursive %s\n",
				format, len(query.Where()), len(query.Args()),
				strings.Count(query.Where(), "WITH RECURSIVE"), source,
			)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(rows); err != nil {
		panic(err)
	}
}
```

## SQL-shape observations

The paired public-generator run produced these two SQL-shape reductions:

| Workload | JSON bytes, baseline → candidate | JSONB bytes |
|:--|--:|--:|
| Empty-seed append followed by type map | 14,937 → 14,243 | 14,954 → 14,260 (+17 bytes) |
| Empty-seed append with guarded item | 8,132 → 7,789 | 8,141 → 7,798 (+9 bytes) |

All 16 SQL outputs kept the same argument lists, and 12 retained byte-identical
SQL and arguments. These observations describe SQL shape only; they do not
measure query execution or runtime speed.

## Validation

The focused checks passed with Go 1.27.1: root unit tests, root vet,
integration vet, the focused SQL race suite, and the real WebAssembly checks
(19 native bridge and 5 JavaScript cases). The related
PostgreSQL selection passed with 67,234 logged query/record outputs across 277
groups, plus 240 independent typed/inferred outputs and 28 canonical numeric
boundary results. The 240 exact outputs and 28 boundary results also matched
the baseline. Eighteen fallback controls retained identical SQL, arguments,
and diagnostics.

The nested-callback growth check covered depths one through eight. It counted
`Context.Err()` calls as a validation-work proxy, not elapsed time or a
complexity guarantee. Baseline counts were 234, 661, 1,377, 2,545, 4,559,
8,506, 17,552, and 41,100; candidate counts were 268, 957, 2,365, 4,950,
9,576, 18,230, 36,173, and 78,393. The extra validation work was about
1.15–2.14 times baseline in these fixtures (about 1.91 times at depth eight).
Disabling the nested list-building validation guard failed the depth-five
bound: 15,413 checks exceeded the baseline-relative limit of 14,677, while the
candidate used 9,576. The baseline itself grows nonlinearly; these observations
make no asymptotic or compile-time speed claim.

Plain `EXPLAIN` was run on PostgreSQL 18.6 with JIT disabled and a 30-second
statement timeout, using `records(labels, offset)` with labels
`{"asset":"YQ==","name":"Alice"}` and offset `2`. The ordinary index-zero
control plan was identical. These optimized and guarded plans changed node
counts in both directions:

| Plan | Node | Baseline → candidate |
|:--|:--|--:|
| Optimized index 4 | Recursive Union / WorkTable Scan / WindowAgg | 2 / 2 / 2 → 0 / 0 / 0 |
| Optimized index 4 | Sort / Aggregate / Nested Loop / Result | 9 / 9 / 32 / 42 → 4 / 11 / 34 / 40 |
| Guard index 7 | Recursive Union / WorkTable Scan / WindowAgg | 2 / 2 / 2 → 1 / 1 / 1 |
| Guard index 7 | Sort / Aggregate / Nested Loop / Result | 6 / 5 / 17 / 23 → 4 / 6 / 18 / 22 |

These are plan-shape observations only. No `EXPLAIN ANALYZE`, benchmark, or
runtime measurement was performed.

The first reproducible program above reports SQL shape. A second program
reproduces the nested validation-work proxy; run it from the checkout at both
the baseline and candidate revisions:

```go
package main

import (
	"context"
	"fmt"

	"github.com/ornew/hff"
	s "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

type counter struct {
	context.Context
	checks int
}

func (c *counter) Err() error {
	c.checks++
	return c.Context.Err()
}

func main() {
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.Field("labels", schema.Struct(
				schema.Bytes("asset"),
				schema.String("name"),
			)),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		panic(err)
	}

	body := "v"
	for depth := 1; depth <= 8; depth++ {
		body = `labels:items:map(p=>p[1]):foldl([],(a,v)=>a:concat([v,` + body + `]))`
		program, err := e.Compile(context.Background(), body+`:exists`)
		if err != nil {
			panic(err)
		}
		ctx := &counter{Context: context.Background()}
		query, err := s.Build(ctx, program,
			s.WithDialect(s.PostgreSQL),
			s.WithBindings(s.BindJSON(
				hff.Path{"labels"},
				s.Column("records", "labels"),
				s.JSON,
			)),
		)
		if err != nil {
			panic(err)
		}
		fmt.Println(depth, ctx.checks, len(query.Where()))
	}
}
```

## Decision and limits

This is a bounded lowering, not a change to HFF fold semantics. It does not
cover nonempty or computed seeds, arbitrary accumulator algebra, nullable or
empty chunks, or callback results that inspect accumulated state. Other forms
retain the existing fallback. Ordinary recursive callback validation remains
necessary and adds work that grows with nested callback depth; the checks above
bound the tested fixture without establishing a general complexity guarantee.
