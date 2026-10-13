# 024. Specialize reachable PostgreSQL JSON container equality

- **Status**: Applied
- **Date**: 2026-10-14

## Problem and mechanism

For root equality and inequality (`=` and `!=`) between two JSON-backed values
with finite compiled root unions containing Lists, Maps, or Structs,
PostgreSQL could normalize both complete values through recursive JSON walks
before comparing them. For bounded schemas, the translator can instead use
the compiled types at each stored position and compare only the container
categories and value families that can occur there.

Lists are compared by ordinal position, with a full join detecting unequal
lengths. Objects are compared by their actual keys with a full join, retaining
Map/Struct cross-shape key-set behavior. Struct fields keep their individual
types; Map fallback values use their merged per-key type where available, so
different fields such as an Int and a Float are not combined into one
ambiguous value type. A nested `Any` value retains the generic recursive
equality path at that position.

Stored-value representability checks and JSONB metadata requirements remain
in force before specialization; consistency between stored values and
sidecar metadata remains the caller's responsibility. Nested Float
normalization failures still contribute the same null state as the general
path. This specialization covers root `=` and `!=`; `in()`, `contains()`, and
`unique()` still use their existing equality paths. The optimization does not
expand support for Bytes, Duration, or Timestamp inside generic JSON
containers.

## Applicability

| Workload | Applied / Partial / Not applied | Reason |
|:--|:--|:--|
| Bounded String/List unions with known element families | Applied | Ordinal equality specializes the element comparison and omits whole-document recursive normalization. |
| Bounded Struct unions with overlapping fields | Applied | Known fields keep per-key types while actual object keys are compared. |
| Map/Struct comparisons with per-key Map fallback | Applied | The fallback remains per key; distinct typed Struct fields are not merged together. |
| Overlapping List unions with metadata-backed Float values | Partial | Equality specializes known positions, but two recursive metadata reconstructions remain. |
| Nested `Any` fields with metadata | Partial | Known fields specialize; unconstrained `Any` leaves retain generic recursion. |
| Unconstrained `Any` at an equality boundary | Not applied | Full dynamic recursive equality remains necessary. |

## Reproduction

The baseline is commit
[1ec85ae](https://github.com/ornew/hff/commit/1ec85ae15635be44633cf70049b693cd65667dd8).
Each shape fixture compiles `left=right` and binds both values to JSONB
columns; the metadata column is added where marked:

| Fixture | Left schema | Right schema | Float metadata |
|:--|:--|:--|:--:|
| `string_list_int` | `String\|List<Int>` | `String\|List<Int>` | No |
| `string_list_int_float` | `String\|List<Int>` | `String\|List<Float>` | No |
| `string_map_struct` | `String\|Map<Int>` | `String\|Struct{a:Int?}` | No |
| `overlap_struct` | `String\|Struct{i:Int,f:Float?}\|Struct{i:Int,label:String?}` | same | No |
| `overlap_list` | `String\|List<Int>\|List<Float>` | same | Yes |
| `nested_any` | `String\|Struct{payload:Any,id:Int}` | same | Yes |

In these Struct schemas, `?` marks an optional field that may be omitted; it
does not make the field nullable.

For example, the first profile's SQL shape can be rebuilt with this small
program:

```go
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func main() {
	ctx := context.Background()
	str := schema.StringType
	leftType := schema.Union(str, schema.ListOf(schema.IntType))
	rightType := schema.Union(str, schema.ListOf(schema.IntType))
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Field("left", leftType),
		schema.Field("right", rightType),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		panic(err)
	}
	p, err := e.Compile(ctx, `left=right`)
	if err != nil {
		panic(err)
	}
	q, err := hffsql.Build(ctx, p,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "left"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "right"), hffsql.JSONB),
		),
	)
	if err != nil {
		panic(err)
	}
	fmt.Printf("WHERE bytes=%d barriers=%d recursive=%d args=%d\n",
		len(q.Where()), strings.Count(q.Where(), "OFFSET 0"),
		strings.Count(q.Where(), "WITH RECURSIVE"), len(q.Args()))
}
```

The SQL-shape test source is `backends/sql/schema_pair_test.go`; the baseline
and candidate shape outputs are retained outside Git at
`/private/tmp/hff-container-pair-{baseline,candidate}.shape`. The shape counts
below report `WHERE` bytes, `OFFSET 0` barriers, recursive JSON walks,
and SQL parameters, in that order.

The focused PostgreSQL/local tests are in
`backends/sql/schema_pair_test.go` and
`backends/sql/schema_pair_integration_test.go`:

```sh
GOTOOLCHAIN=go1.27.1 go test ./backends/sql \
  -run '^TestSchemaPair(UsesBoundedContainerEquality|KeepsAnyLeafFallback|DoesNotMergeUnrelatedNumericFields)$' \
  -count=1
HFF_POSTGRES_CONTAINER=<container> GOTOOLCHAIN=go1.27.1 go test -tags=integration ./backends/sql \
  -run '^TestPostgreSQLSchemaPair(MatchesLocal|FloatFailureIsNestedNull)$' \
  -count=1
GOTOOLCHAIN=go1.27.1 go test ./...
GOTOOLCHAIN=go1.27.1 go vet ./...
```

The new focused integration cases covered 656 JSON/JSONB pair comparisons and
12 Float-failure comparisons. Existing dynamic-pair, metadata, physical
spelling, and Bytes-tagged controls also passed. These checks establish
semantic parity, not runtime speed.

For plan-shape inspection, the `String|List<Int>` profile used a temporary
table with JSONB `left` and `right` columns and these five rows:
`('"x"','"x"')`, `('[1,2]','[1,2]')`, `('[1]','[2]')`, `('[]','[]')`, and
`(NULL,NULL)`. The query was
`EXPLAIN (COSTS OFF) SELECT 1 FROM records WHERE <generated WHERE>`; it did not
use `ANALYZE`. The baseline plan had 69 lines and 18 Nested Loop
nodes; the candidate had 21 lines and 5 Nested Loop nodes. Both plans retained
two `jsonb_array_elements` scans. The plan script and captures are retained
outside Git at `/private/tmp/hff-schema-pair-explain.go` and
`/private/tmp/hff-schema-pair-{baseline,candidate}.plan`. These are plan-shape
observations only; no execution-time measurement was collected.

## Results

| Shape | Baseline `WHERE` bytes / barriers / recursive walks / args | Candidate bytes / barriers / recursive walks / args |
|:--|--:|--:|
| String/List<Int> | 6,611 / 18 / 1 / 0 | 2,044 / 6 / 0 / 0 |
| String/List<Int> vs String/List<Float> | 7,784 / 18 / 1 / 0 | 3,335 / 13 / 0 / 0 |
| String/Map/Struct | 6,748 / 18 / 1 / 1 | 2,101 / 6 / 0 / 1 |
| Overlapping Struct alternatives | 10,359 / 18 / 1 / 6 | 3,959 / 8 / 0 / 3 |
| Overlapping List alternatives with metadata | 12,857 / 18 / 3 / 0 | 8,634 / 18 / 2 / 0 |
| Nested Any with metadata | 10,573 / 18 / 3 / 4 | 9,920 / 16 / 3 / 2 |

These are generated SQL-shape comparisons. No timing, benchmark, or execution
speed claim is made.

## Correctness and tradeoffs

Parity coverage includes ordered list comparisons, empty and unequal-length
lists, Map/Struct cross-shape keys, missing versus stored null, null roots,
null/no-value observations, nested Any leaves, metadata-backed Float values,
and Float normalization failures that become nested nulls. Tests also ensure
that an Int field and a Float field at different Struct keys remain
independently typed. Schema representability and metadata requirements remain
in force. Stored document and sidecar consistency is a caller contract, not a
row validation performed by this path.

Some recursive work remains where a stored position is `Any`, where multiple
overlapping alternatives require metadata restoration, or where the general
path is required. Reductions in generated SQL do not establish execution
speed or a PostgreSQL plan for other schemas or data.

## Decision and limits

Use bounded recursive equality for reachable JSON container positions while
retaining generic equality at unconstrained values. Preserve the existing
normalization, metadata, key-set, order, and null contracts. Do not extend the
result to unsupported container value types or infer runtime gains from SQL
size alone.
