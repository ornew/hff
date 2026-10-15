# 060. Share repeated PostgreSQL flatmap expressions

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

A `flatmap()` callback may emit repeated pure expressions among other list
leaves. Lowering each repeated copy independently repeats row expansion and
its SQL dependencies. A candidate PostgreSQL lowering validates the original
constructor, then groups repeated leaves by strict typed-tree identity and
shares one ordered row source for each eligible group.

Identity includes node kind and resolved type, no-value capability, lexical
binding identity and source path, typed literal value (including Float bits),
operator and field details, guard constraints, resolved built-in method
signature, child order, and backend order demand. Each shared expression must
contain at least one bound lexical reference; it may be a captured outer
binding. Unbound literal leaves remain independent. The proof does not use
SQL-text or result-type equality. Callback nodes, custom methods, unsupported
nodes, and unsupported literal forms make the whole callback list use
ordinary lowering. A shared row source preserves each group's original output
positions, order, null values, no-value failure from any leaf, captures, and
first descriptor. The position map remains separate, so later literal indexing
can select the corresponding original descriptor without reconstructing all
row alternatives. Guarded-list normalization remains in its owning descriptor
scope. A subsequent transformation discards this literal-position view; its
consumer must use a separately supported lowering path. The original
constructor validation still runs before the shared source is used.

## Applicability

| Callback list expression | Status | Reason |
|:--|:--|:--|
| `[p[1],p[1]]` | Candidate | Both leaves have the same typed slot expression and binding. |
| `[p[1],p[0],p[1]]` | Candidate | The repeated value-slot group is shared across a distinct key-slot leaf. |
| `[p[1],p[0],p[1],p[0]]` | Candidate | Both expression groups retain their original positions. |
| `[p[1],[],p[0],p[1],null]` | Candidate | The repeated slot is grouped; unbound empty and null leaves remain independent. |
| `[p[1]:[bytes],p[1]:[bytes]]` | Candidate | Identical guard and source identity are proven. |
| `[p[1]:base64,p[1]:base64]` | Candidate | The built-in method and its signature match. |
| `[p[1],p[1],p[1]]` | Candidate | The repeated expression retains its identity across all leaves. |
| `[p[1],q[1]]` for distinct bindings | Not applied | Descriptor or SQL-text equality cannot replace binding identity. |
| Nested callback or custom-method leaves | Not applied | This proof does not equate callback or extension behavior. |
| Local backend | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [92803d8](https://github.com/ornew/hff/commit/92803d856a4dc6ff1035c3d360e75bcabc2afc8f).
The candidate implementation accompanies this record and is based on that
revision. The public API generator uses Go 1.27.1 and emits parameterized
`WHERE` SQL and ordered arguments for JSON and JSONB; it does not execute
queries or measure runtime performance.

Save the 14-case audit program as `/private/tmp/hff-repeat-mixed-audit.go`:

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
		`labels:items:flatmap(p=>[p[1],p[1]]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1],p[1],p[1]]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1]]:concat([p[1]])):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1]!,p[1]!]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1]:[bytes],p[1]:[bytes]]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[0]:len+1,p[0]:len+1]):json:exists`,
		`labels:items:flatmap(p=>[p[1]:base64,p[1]:base64]):json:exists`,
		`labels:items:flatmap(p=>[[p[1]],[p[1]]]):flatmap(v=>v):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[{"payload":p[1]},{"payload":p[1]}]):map(v=>v.payload:type):json:exists`,
		`labels:items:flatmap(p=>[p[labels:size-1],p[labels:size-1]]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1],p[1]]):flatmap(v=>[v,v]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1],p[0],p[1]]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1],p[0],p[1],p[0]]):map(v=>v:type):json:exists`,
		`labels:items:flatmap(p=>[p[1],[],p[0],p[1],null]):map(v=>v:type):json:exists`,
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

Run the same generator from the baseline and candidate checkouts:

```sh
GOTOOLCHAIN=go1.27.1 go run /private/tmp/hff-repeat-mixed-audit.go \
  > /private/tmp/hff-repeat-mixed-before.json \
  2> /private/tmp/hff-repeat-mixed-before.log

GOTOOLCHAIN=go1.27.1 go run /private/tmp/hff-repeat-mixed-audit.go \
  > /private/tmp/hff-repeat-mixed-after.json \
  2> /private/tmp/hff-repeat-mixed-after.log
```

The 14-case audit compared generated SQL size and argument counts; the matching
raw JSON and log files are retained under `/private/tmp/hff-repeat-mixed-*`.
These observations describe SQL shape, not runtime performance.

## Results

Generated `WHERE` byte counts, baseline → candidate:

| Expression shape | JSON | JSONB |
|:--|--:|--:|
| Repeated `p[1]` slot | 8,069 → 5,062 | 8,075 → 5,068 |
| Triple `p[1]` slot | 12,121 → 5,070 | 12,127 → 5,076 |
| Repeated slot in strict concat | 8,095 → 5,062 | 8,101 → 5,068 |
| Repeated guarded slot | 8,500 → 5,272 | 8,506 → 5,278 |
| Repeated type-guarded slot | 8,608 → 5,326 | 8,614 → 5,332 |
| Repeated base64 conversion | 11,802 → 5,996 | 11,808 → 6,002 |
| Repeated nested-list expression | 10,452 → 6,179 | 10,458 → 6,185 |
| Repeated object expression | 8,069 → 5,062 | 8,075 → 5,068 |
| Repeated nested flatmap chain | 10,502 → 6,204 | 10,508 → 6,210 |
| Mixed slot/key/slot leaves | 12,123 → 8,113 | 12,129 → 8,119 |
| Alternating slot/key leaves | 17,197 → 8,121 | 17,203 → 8,127 |
| Mixed slots with empty and null literals | 17,243 → 12,247 | 17,249 → 12,253 |
| Key-length arithmetic control | 3,676 → 3,676 | 3,680 → 3,680 |
| Dynamic-index control | 14,795 → 14,795 | 14,816 → 14,816 |

The audit did not measure query execution, allocations, or runtime. The
key-length arithmetic and dynamic-index controls retained their original SQL
shape. Plain `EXPLAIN` (without `ANALYZE`) was collected for repeated
slot, triple-slot, base64, nested, object, and nested-chain workloads on
PostgreSQL 18.6, with JIT disabled and a 30-second statement timeout. The
fixture used `records(labels)` with `{"app":"YQ==","z":null}`. Baseline →
candidate counts for Function Scan / Append / Aggregate / Values Scan /
Materialize were:

| Workload | Function Scan | Append | Aggregate | Values Scan | Materialize |
|:--|--:|--:|--:|--:|--:|
| Repeated slot | 7 → 5 | 2 → 0 | 4 → 4 | 0 → 2 | 0 → 2 |
| Triple slot | 9 → 5 | 2 → 0 | 4 → 4 | 0 → 2 | 0 → 2 |
| Base64 conversion | 5 → 4 | 1 → 0 | 3 → 3 | 0 → 1 | 0 → 1 |
| Nested-list expression | 9 → 6 | 3 → 0 | 4 → 4 | 0 → 3 | 0 → 3 |
| Object expression | 7 → 5 | 2 → 0 | 4 → 4 | 0 → 2 | 0 → 2 |
| Nested repeated chain | 9 → 6 | 3 → 0 | 4 → 4 | 4 → 5 | 4 → 5 |
| Mixed slot/key/slot leaves | 9 → 7 | 2 → 2 | 4 → 4 | 0 → 2 | 0 → 2 |
| Alternating slot/key leaves | 11 → 7 | 2 → 2 | 4 → 4 | 0 → 4 | 0 → 4 |
| Mixed slots with empty and null literals | 13 → 11 | 2 → 2 | 4 → 4 | 0 → 2 | 0 → 2 |

These are plan-shape observations only; they do not establish execution counts
or runtime improvement.

## Correctness and tradeoffs

The optimization applies only when strict typed-tree identity proves repeated
leaves equivalent and each shared group contains a bound lexical reference,
which may be a capture. Other expressions and unbound literals remain in their
original positions. The published 14-case generator reproduced all 28 JSON and
JSONB SQL-and-argument records. The related PostgreSQL selection passed
230,532 logged query/record results across 418 groups. The 89-source by
19-fixture JSON/JSONB matrix is included in that count, with negative Build
cases excluded. Separate exact-output and raw-Float boundary assertions also
passed; those are not included in the logged total. Build tests checked 112
negative combinations and confirmed their diagnostics against the baseline.
Root unit, vet, integration vet, SQL race, and WASM checks passed. This selected
matrix is not a claim of full backend conformance. Logs are retained at
`/private/tmp/hff-repeat-mixed-final2-pg.log` and
`/private/tmp/hff-repeat-mixed-final2-checks.log`.

The plain `EXPLAIN` rows above compare plan shape, not execution counts or
runtime. No benchmark or `EXPLAIN ANALYZE` was run.

## Decision and limits

Extend sharing to repeated groups that meet the proof above while retaining
the existing parameter-only path. Keep ordinary lowering for unsupported
callback lists; this is not general common-subexpression elimination across
arbitrary callbacks or transformations. The earlier parameter-only evidence
remains in the [parameter record](059-postgresql-repeated-flatmap-parameters.md).
