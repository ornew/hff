# 064. Carry native numeric inputs into PostgreSQL folds

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-16

## Summary

For eligible deferred folds, PostgreSQL carries numeric input arms as native
`bigint` or `double precision` values instead of serializing and decoding
those arms through JSON. Eligibility is checked per arm, so separate arms may
have different numeric kinds. The fold accumulator keeps its existing
representation.

## Problem and mechanism

A deferred item pipeline can produce numeric expressions that already have a
native SQL representation. Encoding each input arm as JSON and decoding it
again inside the fold repeats conversion work and enlarges generated SQL. This
path carries each eligible arm in its native SQL type while preserving the
fold's existing accumulator state.

The check is per emitted arm. The arm's stored HFF family must contain only
one non-null numeric kind, optionally nullable, matching the expression's SQL
kind: Int uses `bigint`, and Float uses `double precision`. Different arms
may independently use Int or Float. The specialized path is selected only
when every emitted arm qualifies; if any arm fails the proof, the whole fold
retains its original JSON input transport. A single arm with `Any` or a wider
union, or an expression whose SQL kind does not match that arm's stored
family, uses the existing JSON path. Numeric identity is never inferred from
an ambiguous stored value.

Existing native and safe identity-fold paths retain priority. The source,
seed, and callback are fully validated; custom translators anywhere in them
remain outside this path. Empty arms keep generic validation. Source order,
presence, null/no-value behavior, Float provenance, and callback semantics
are preserved. JSONB metadata requirements are unchanged. The accumulator
stays on the existing JSON state path, and the transport does not change the
public binding format.

## Applicability

| Emitted input arm | Behavior | Reason |
|:--|:--|:--|
| Int expression emitted as `bigint` | Applied | It matches the arm's single stored Int family. |
| Float expression emitted as `double precision` | Applied | It matches the arm's single stored Float family. |
| Separate Int and Float arms | Applied | Each arm is checked and carried independently. |
| Nullable Int or Float arm with matching SQL kind | Applied | Nullability does not change its non-null numeric family. |
| One arm with `Any` or multiple possible numeric families | Not applied | That arm does not establish one stored numeric family. |
| SQL expression kind differs from its stored family | Not applied | Native representation must match the stored family. |
| Existing native or safe identity-fold case | Earlier path | This optimization does not replace established fold lowering. |

## Reproduction

The SQL-shape baseline is [35a31ee](https://github.com/ornew/hff/commit/35a31ee70efde6e61cde90f1b164b90e25a44856). The public API generator below builds SQL predicates for JSON and JSONB, reporting predicate size, argument count, and array-constructor calls. It does not execute a database query.

Save it as `/tmp/native-fold-audit.go` outside the checkout and run
`GOTOOLCHAIN=go1.27.1 go run /tmp/native-fold-audit.go` from the HFF repository
root at the baseline and implementation revisions.

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
		`labels:items:map(p=>p[1]):flatmap(v=>[1,2.0]):foldl(0.0,(a,v)=>a+v)>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type:len,v:type:len/2]):foldl(0.0,(a,v)=>a+v)>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:string:len,v:string:len/2]):foldl(0.0,(a,v)=>a+v)>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type:len,v:type:len+1]):foldl(0,(a,v)=>a+v)>0`,
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
				fmt.Fprintf(os.Stderr, "REJECT %s %s: %v\n", format, source, err)
				continue
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

## Results

For JSON, the two eligible workloads each shortened by 681 bytes. JSONB
results were 9 bytes larger than their corresponding JSON results.

| Workload | Baseline → implementation bytes | Arguments |
|:--|--:|--:|
| Literal Int and Float arms `[1,2.0]` | 11,920 → 11,239 | 7 |
| Per-item Int and Float projections | 16,606 → 15,925 | 10 |
| Same-kind Int control `[len,len+1]` | 13,270 → 13,270 | 10 |

The control SQL and arguments were unchanged. The `string():len` variant was
rejected by `Build` at both revisions. The final focused PostgreSQL run passed
13,272 query/record outputs across 40 fixture groups, including 21 source
forms across JSON and JSONB. A separate 272 typed and inferred expected
outputs also passed. The six audited SQL outputs retained their argument
lists. Related fixed-list, observed-field, and unused-input fold matrices
contributed 16,360, 11,376, and 13,904 further outputs, respectively; these
categories are reported separately.

Root unit, root vet, integration vet, focused SQL race, and WASM checks
passed. The focused race check covered the fold tests, not the entire SQL
package, and took 32.711 seconds. The relevant focused commands are:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^(TestNativeNumericDeferredFoldsBuild|TestNativeNumericFold)' -count=1

: "${HFF_POSTGRES_CONTAINER:?set to a running PostgreSQL container name or ID}"
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLNativeNumericFold' -count=1
```

No benchmark or `EXPLAIN ANALYZE` result is reported.

Plain `EXPLAIN` on PostgreSQL 18.6 with JIT disabled and a 30-second timeout
showed unchanged Function Scan, Aggregate, Sort, and Recursive Union counts.
For two eligible JSON workloads, Nested Loop counts changed 47→45 and 56→54,
while Result counts changed 53→51 and 62→60. These are plan-shape observations
only; no `EXPLAIN ANALYZE`, benchmark, or runtime-speed result is reported.

## Correctness and tradeoffs

The selected fold inputs retain their typed native values, while order,
presence, null/no-value state, Float provenance, and callback behavior remain
unchanged. The accumulator continues to use its existing representation.
Input kinds outside the per-arm proof retain the existing JSON lowering.
Empty arms also retain their generic validation path.

## Decision and limits

The implementation is limited to deferred folds whose emitted numeric arms
each have one stored family matching the native SQL kind. It does not add a
public storage type or change fold semantics. Broader native input transport
is outside this record.
