# 065. Carry native scalar inputs into PostgreSQL folds

- **Status**: Implemented
- **Author**: @ornew
- **Date**: 2026-10-16

## Summary

Eligible deferred PostgreSQL folds carry emitted scalar input arms in native
SQL columns rather than encoding and decoding those arms through JSON. Each
arm is checked independently, and separate arms may use different scalar
families. The fold accumulator keeps its existing JSON representation.

## Problem and mechanism

A deferred fold may receive inputs that already have direct PostgreSQL scalar
expressions. Sending those expressions through a JSON packet adds serialization
and decoding SQL. This bounded path keeps a matching native SQL value for each
eligible input arm while preserving the existing accumulator plan.

An arm qualifies only if it is a direct scalar expression whose stored HFF
family is exactly one non-null scalar family matching the SQL expression kind;
nullable forms of that same family also qualify. The supported column layout
is:

| Stored HFF family | Native SQL column |
|:--|:--|
| Int | `bigint` |
| Float | `double precision` |
| Bool | `boolean` |
| String | `text` |
| Bytes | `bytea` |
| Duration | `bigint` in a distinct typed column |
| Timestamp | `numeric` with exact epoch nanoseconds |

Each emitted arm is proved separately, so one fold may carry, for example,
String and Bool arms or Bytes and Int arms in separate columns. If any nonempty
arm fails the proof, the whole fold keeps its original JSON input transport;
there is no partial native-column plan. An `Any`, multi-family, or otherwise
unrecoverable arm, and a mismatch between the expression kind and stored
family, use the existing JSON path. Selected object fields and fixed-list
positions still using packet transport are outside this slice.

Existing native and safe identity-fold paths retain priority. Empty arms keep
their existing validation path. The full source, seed, and callback remain
validated, and custom translators anywhere in them remain outside this path.
Input order, presence, null/no-value behavior, and Float provenance are
preserved. JSONB metadata requirements are unchanged. Only the input transport
changes; the accumulator remains JSON and the public binding format is
unchanged.

## Applicability

| Emitted input arms | Behavior | Reason |
|:--|:--|:--|
| Nullable String and Bool arms with matching SQL kinds | Applied | Each arm has one matching stored family and uses its own native column. |
| Bytes and Int arms with matching SQL kinds | Applied | The arms keep distinct `bytea` and `bigint` representations. |
| Duration and Int arms | Applied | Both use `bigint` physically but occupy distinct typed columns. |
| Timestamp and Int arms | Applied | Timestamp uses exact epoch-nanosecond `numeric`; Int remains `bigint`. |
| One arm with `Any` or multiple possible stored families | Not applied | A single native representation is not proven for that arm. |
| Any emitted arm has mismatched expression and stored kinds | Not applied | The whole fold keeps the JSON transport. |
| Selected object field or fixed-list position still represented by a packet | Not applied | This input projection remains on the existing packet path. |
| Existing native or safe identity-fold case | Earlier path | This optimization does not replace established lowering. |

## Reproduction

The SQL-shape baseline is
[c7f0442](https://github.com/ornew/hff/commit/c7f04429ee17f92c60cfb546eb52ae13318018a2).
The public API generator below builds four expressions against JSON and JSONB
bindings, reporting each predicate, argument list, and SQL-shape counts. It
does not execute a database query.

Save it as `/tmp/native-scalar-fold-audit.go` outside the checkout and run
`GOTOOLCHAIN=go1.27.1 go run /tmp/native-scalar-fold-audit.go` from the HFF
repository root at the baseline and implementation revisions.

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
		`labels:items:map(p=>p[1]):flatmap(v=>[v:type,v:type:len>4]):foldl("",(a,n)=>a:concat(n:string)):len>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>["YQ==":frombase64,v:type:len]):foldl("",(a,n)=>a:concat(n:string)):len>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>["1s":duration,v:type:len]):foldl("",(a,n)=>a:concat(n:string)):len>0`,
		`labels:items:map(p=>p[1]):flatmap(v=>["2026-01-01T00:00:00Z":timestamp,v:type:len]):foldl("",(a,n)=>a:concat(n:string)):len>0`,
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

## Results

The baseline is
[c7f0442](https://github.com/ornew/hff/commit/c7f04429ee17f92c60cfb546eb52ae13318018a2),
which produces the same SQL for these four audit expressions as its parent.
For JSON, the generated predicates changed as follows; corresponding JSONB
predicates were 9 bytes larger.

| Workload | JSON bytes, baseline → implementation | JSONB bytes |
|:--|--:|--:|
| String and Bool arms | 12,638 → 12,381 | 12,647 → 12,390 |
| Bytes and Int arms | 11,454 → 10,243 | 11,463 → 10,252 |
| Duration and Int arms | 19,024 → 11,109 | 19,033 → 11,118 |
| Timestamp and Int arms | 15,924 → 11,133 | 15,933 → 11,142 |

All eight generated SQL outputs retained their argument lists. The Duration/Int
workload used four recursive-union sites at baseline and one in the
implementation. These are SQL-shape observations, not runtime measurements.
The focused PostgreSQL matrix passed 15,800 query/record results across 25
native-scalar source forms; 432 additional typed/inferred expected outputs
passed, including a same-fold union containing all seven supported families.
The broader related run logged 70,712 results across 200 fixture
groups, including that focused matrix. Previous numeric, fixed-list,
observed-field, and unused-input fold matrices contributed 13,272, 16,360,
11,376, and 13,904 results, respectively; those are separate suites.

Plain `EXPLAIN` on PostgreSQL 18.6 with JIT disabled and a 30-second timeout
showed Nested Loop counts changing 49→47, 46→44, 57→49, and 52→50; Result
counts changed 53→51, 50→48, 55→53, and 56→54. For the Duration/Int workload,
Recursive Union changed 4→1, Aggregate 12→3, Function Scan 9→6, CTE Scan
18→6, and WorkTable Scan 4→1. The other three workloads retained their
Function Scan, Aggregate, Sort, and Recursive Union counts. These are plan
shape observations only; they do not establish runtime speed.

## Verification

The focused native-scalar unit and PostgreSQL tests passed, including 432
independent typed/inferred expected outputs. Root unit tests, root vet,
integration vet, the fold-focused SQL race, and WASM checks (19 native and 5
JavaScript cases) passed. The race covered the fold tests only, not the full SQL
package. A depth-five negative control that re-enabled nested pruning inside the
original-path validation probe failed after 8,486 checkpoints; the guarded
implementation passed that case with 3,216 checkpoints. No benchmark or `EXPLAIN ANALYZE` result is reported.

The bounded PostgreSQL correctness run disabled JIT and set a 30-second
statement timeout in fixture sessions; it did not modify the generated SQL.

Focused commands:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^TestNativeScalarDeferredFold' -count=1

: "${HFF_POSTGRES_CONTAINER:?set to a running PostgreSQL container name or ID}"
GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLNativeScalarFold' -count=1
```

## Correctness and tradeoffs

The typed input columns preserve each arm's stored family while keeping order,
presence, null/no-value state, source evaluation, Float provenance, and
callback behavior unchanged. The accumulator keeps its existing JSON
representation. If any emitted arm fails the proof, the entire fold retains
the JSON input transport. This path adds no public binding type or value
format.

## Decision and limits

Native input transport is limited to direct scalar arms whose SQL kind matches
one stored HFF family. It does not cover packet-based selected object fields
or fixed-list positions, `Any` or multi-family arms without a separate proof,
or custom translators in the source, seed, or callback. The existing JSON
transport remains the fallback.
