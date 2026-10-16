# 066. Carry selected children through PostgreSQL deferred folds

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-16

## Summary

This bounded PostgreSQL fold path carries selected children from constructed
object and fixed-list roots in native typed columns. It keeps the existing JSON
accumulator and full validation while avoiding packet transport for selected
children when each emitted root and observed value has a recoverable type.

## Problem and mechanism

The existing deferred-fold packet path encodes constructed objects or fixed
lists before the callback selects a child. A callback that reads a known child
can carry that value directly, together with its SQL kind, instead of encoding
and decoding unrelated children.

The proof applies when the fold input is a constructed object whose callback
reads a first-level field or literal-string key, or a fixed list whose callback
reads a finite integral numeric literal index. Negative indexes resolve to
their semantic positions in the literal list. Every nonempty root must match
the expected object or list constructor. Each observed child must have one
stored HFF family matching its native SQL kind. If any observed arm or root
fails, the whole fold keeps its JSON packet transport.

The transport uses dense selected-child slots and kind columns across emitted
arms. It retains original keys, field order, semantic list positions, repeated
children, and selected type/Float-loss metadata. The decoder reconstructs the
object/list shape with selected typed values; unselected fields and positions
use dummy entries rather than carrying their original payloads. Unselected
slots do not widen the dense layout, but source-global presence remains part of
the fold state: an unobserved no-value still invalidates the result. Missing
object keys and out-of-range indexes remain ordinary null when the fold also
has at least one proven native selection; a missing-only or out-of-range-only
fold uses the packet path. A nullable fixed-list root carries an
`input_null` Boolean; coalescing it to false for the row selector preserves
the previous CASE behavior when the test is unknown.

The added probe reuses the prepared source plan. The original source, seed,
and complete callback are still validated with nested pruning disabled before
the selected plan is adopted. There is no AST rewrite or re-evaluation. Custom
translators anywhere in the source, seed, or callback use the existing path.
Native and safe identity-fold paths retain priority, the accumulator remains
JSON, and this private transport does not change the public binding format.

## Applicability

| Fold input and callback | Behavior | Reason |
|:--|:--|:--|
| Constructed object, callback reads a first-level field or literal-string key | Eligible | The selected child has a recoverable stored family. |
| Fixed literal list, callback reads a finite integral literal index | Eligible | The index maps to a semantic position in the known list shape. |
| Multiple selected children with different matching families | Eligible | Dense slots retain per-child kind information. |
| Missing object key or out-of-range literal index, alongside another proven native selection | Eligible | The selected child retains ordinary-null behavior. |
| Missing-key-only or out-of-range-only observation | Fallback | No native child was selected to project. |
| Any observed child or nonempty root without a matching proof | Fallback | The complete fold retains its JSON packet. |
| Whole-root guard, unobserved shape, or unsupported dynamic access | Fallback | The selected-child proof does not apply. |
| Custom translator in source, seed, or callback | Fallback | Full validation and existing lowering remain in force. |
| Existing native or safe identity-fold path | Earlier path | This work does not replace established lowering. |

## Reproduction

The comparison baseline is
[9772bf5](https://github.com/ornew/hff/commit/9772bf5ae7e354fdd5e7dd201427e9c06a4698ef).
The public API generator below builds seven expressions against JSON and
JSONB bindings. It records SQL and arguments for the five supported shapes
and reports the two existing Build rejections; it does not execute a database
query.

Save the source as `/tmp/native-child-fold-audit.go` outside the checkout and
run `GOTOOLCHAIN=go1.27.1 go run /tmp/native-child-fold-audit.go` from the HFF
repository root at the baseline and candidate revisions.

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
		`labels:items:map(p=>{"used":p[1]:type:len,"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.used)>0`,
		`labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}]):foldl(0,(a,v)=>a+v[0])>0`,
		`labels:items:map(p=>{"kind":p[1]:type,"present":p[1]:exists,"unused":[p[1],p[1]]}):foldl(0,(a,v)=>a+v.kind:len+v.present:string:len)>0`,
		`labels:items:map(p=>["1ns":duration,"0000-01-01T00:00:00.000000001Z":timestamp,p[1]]):foldl(true,(a,v)=>a v[0]:nanoseconds=1 v[1]:year=0)`,
		`labels:items:map(p=>[p[1]:type:len,p[1]:type:len/2]):foldl(0,(a,v)=>a+v[0])>0`,
		`labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}]):foldl(0,(a,v)=>a+v[0]:[int])>0`,
		`labels:items:map(p=>[p[1]:type:len,{"unused":p[1]}]):foldl(0,(a,v)=>a+v[0]:type:len)>0`,
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
				fmt.Fprintf(os.Stderr, "REJECT %s %s %v\n", format, source, err)
				continue
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

## Results and verification

The comparison baseline is
[9772bf5](https://github.com/ornew/hff/commit/9772bf5ae7e354fdd5e7dd201427e9c06a4698ef).
The five supported JSON predicates changed as follows; the corresponding
JSONB predicates were 9 bytes larger:

| Workload | JSON bytes, baseline → implementation | JSONB bytes |
|:--|--:|--:|
| Constructed object with one selected field | 7,764 → 7,633 | 7,773 → 7,642 |
| Constructed object with selected kind and presence fields | 9,047 → 8,830 | 9,056 → 8,839 |
| Fixed-list input with Int and Float positions | 20,461 → 20,200 | 20,470 → 20,209 |
| Selected fixed-list child with an Int guard | 10,327 → 9,421 | 10,336 → 9,430 |
| Selected fixed-list child with a type observer | 8,933 → 8,584 | 8,942 → 8,593 |

The public generator emits ten supported SQL/argument outputs. It also
reproduces four expected Build rejection lines for two existing unsupported
source forms; those remain rejected. The expanded PostgreSQL suite passed
88,408 logged query/record results across 240 groups and 600 separate exact
typed/inferred results. The exact cases include all seven scalar families in
one object, all 256 Bytes values, time and duration limits, missing/null/no-
value cases, negative indexes, captures, and same-kind distinct values.
Previous checks for native scalar inputs, numeric inputs, fixed-list inputs,
observed object fields, and unused input payloads also passed.

Plain `EXPLAIN` on PostgreSQL 18.6, with JIT disabled and a 30-second timeout,
used the same `records(labels, offset)` fixture for five JSON shapes, with
`labels` equal to `{"asset":"YQ==","name":"Alice"}` and `offset` equal to 2.
Nested Loop counts changed 32→31, 41→39, 45→44, 36→35, and 33→32; Result
counts changed
34→33, 43→41, 53→52, 38→37, and 35→34. Each plan retained one Recursive
Union, one WorkTable Scan, three Sort nodes, and one WindowAgg. These are plan
shape observations only, not execution-time evidence.

The depth-six validation test passed with 3,864 checkpoints; disabling the
nested-pruning guard failed its bound after 13,890 checkpoints. The
shape-rejection and nested-validation negative controls also failed as
expected. Root unit tests, root vet, integration vet, the fold-focused SQL
race, and real WASM checks (19 native and 5 JavaScript cases) passed. The race
covered the focused fold tests, not the full SQL package.

Focused commands:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^TestNativeChild' -count=1

export HFF_POSTGRES_CONTAINER="${HFF_POSTGRES_CONTAINER:?set to a running PostgreSQL container name or ID}"
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLNativeChildFold' -count=1
```

No benchmark or `EXPLAIN ANALYZE` result is reported.

## Limitations

This proof covers selected children from constructed object and fixed-list
roots within the existing deferred-fold pipeline. It does not broaden general
root or stored-union recovery, accept unknown or ambiguous child types, change
ordinary Build refusals, or change JSONB metadata requirements. If any part of
the proof fails, the whole fold uses the existing JSON packet path.
