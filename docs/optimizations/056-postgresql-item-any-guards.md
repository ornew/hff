# Simplify unconstrained PostgreSQL JSON item-payload guards

- **Status**: Implemented
- **Date**: 2026-10-16

## Problem and mechanism

A recovered object `items()` pair has a String key and a schema-known value.
When the guard accepts `Any` for every pair element, each payload predicate is
always true. The bounded lowering omits per-key payload dispatch while keeping
Build-time validation that declared keys are valid PostgreSQL text, pair null
checks, and outer-list scans. It does not inspect payload types or change
normalization, Float, or representation rules.

## Applicability

| Execution profile | Applied / Partial / Not applied / Not applicable | Reason |
|:--|:--|:--|
| JSON/JSONB item-pair guards accepting `Any` | Partial | Per-key payload dispatch is omitted; key validation and pair/list checks remain. |
| Payload-specific guards | Not applied | The active value type still determines the predicate. |
| Key-only traversal | Not applicable | It uses a separate lowering path. |
| Local | Not applicable | This is a PostgreSQL SQL-lowering choice. |

## Reproduction

The baseline is [2c2f7ed](https://github.com/ornew/hff/commit/2c2f7edfbc310d165b15851be570c6b4b44df4a6). The comparison uses Go 1.27.1. Save this program as `/private/tmp/hff-item-any-guard-audit.go`:

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
		fields = append(fields, schema.Optional(schema.Nullable(schema.Bytes(fmt.Sprintf("label%02d", i)))))
	}
	typ := schema.Struct(schema.Optional(schema.Nullable(schema.Field("labels", schema.Struct(fields...)))))
	e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	sources := []string{
		`labels:items:any(p=>p:[list]:exists)`,
		`labels:items:map(p=>p:[list]):size>0`,
		`labels:items:[list[list]]:exists`,
		`labels:items[0]:[list]:exists`,
		`labels:items:any(p=>p:[list[string]]:exists)`,
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
				hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), format)),
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
			fmt.Fprintf(os.Stderr, "%s %dB %dargs %s\n", format, len(query.Where()), len(query.Args()), source)
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		panic(err)
	}
}
```

Run it from each checkout, changing the output suffix from `before` to
`after` in the candidate checkout:

```sh
go run /private/tmp/hff-item-any-guard-audit.go \
  > /private/tmp/hff-item-any-guard-before.json \
  2> /private/tmp/hff-item-any-guard-before.log
```

The unit and PostgreSQL parity checks are:

```sh
go test ./backends/sql -run '^TestUnconstrainedItemGuards' -count=1
HFF_POSTGRES_CONTAINER=<container-name-or-id> \
  go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLUnconstrainedItemGuardsMatchLocal$' -count=1
```

The JSON and JSONB logs are `/private/tmp/hff-item-any-guard-before.log` and
`/private/tmp/hff-item-any-guard-after.log`. The complete SQL observations are
in the matching `.json` files.

## Results

The observations compare generated `WHERE` size and argument count. The
four representative workloads cover callback, map, whole-list, and first-pair
guards. Source-constrained String guards and key-only maps retain byte-identical
SQL and arguments.

| Workload | Format | Baseline bytes / args | Candidate bytes / args | Change |
|:--|:--|--:|--:|:--|
| Any payload guard | JSON | 13,654 / 24 | 11,872 / 12 | −1,782 bytes / 12 args |
| Map payload guard | JSON | 14,067 / 25 | 12,285 / 13 | −1,782 bytes / 12 args |
| Whole-list guard | JSON | 13,769 / 24 | 11,915 / 12 | −1,854 bytes / 12 args |
| First-pair guard | JSON | 13,423 / 25 | 11,665 / 13 | −1,758 bytes / 12 args |
| Any payload guard | JSONB | 13,683 / 24 | 11,901 / 12 | −1,782 bytes / 12 args |
| Map payload guard | JSONB | 14,096 / 25 | 12,314 / 13 | −1,782 bytes / 12 args |
| Whole-list guard | JSONB | 13,798 / 24 | 11,944 / 12 | −1,854 bytes / 12 args |
| First-pair guard | JSONB | 13,452 / 25 | 11,694 / 13 | −1,758 bytes / 12 args |

Plain `EXPLAIN` without `ANALYZE` showed 37 lines before and after for the
callback guard, and 31 lines before and after for the whole-list guard. The
subplan counts did not change. This is SQL-shape evidence only; it does not
establish runtime performance.

The final related PostgreSQL run logged 208,912 query/record outputs across
609 groups. The new item-guard matrix covered 15,044 outputs across 36 groups.
The unit suite retained 28 source cases. Twelve Build-only assertions cover two
mixed String/tagged materialization queries across three tagged families and
both JSON formats; those rejected queries were not executed by PostgreSQL.
These materialization boundaries are not changed by this optimization.

## Correctness and tradeoffs

The rewrite is safe only when the guard accepts `Any` for every pair element.
It retains declared-key text validation, pair and outer-list null behavior,
source evaluation, and fallback lowering for payload-specific guards. The
omitted per-key payload dispatch does not remove the pair/list scans or their
validation work. Some guarded mixed String/tagged values still cannot be
materialized because their original per-key identity is not retained; that is
a separate support gap.

## Decision and limits

Apply the simplification only to unconstrained item-payload guards. Keep normal
lowering for typed targets and fallback cases. SQL-size and plain-plan
observations do not establish runtime performance; broader guard
canonicalization remains to be audited.
