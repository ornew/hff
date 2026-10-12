# Private WebAssembly Playground Bridge

`playground/` is a private bridge for the documentation-site playground, not
a supported JavaScript SDK. Its Go `main` package uses the same public engine,
schema, value, capability, and local-filter APIs as native HFF. The browser
entry point is build-tagged for `js/wasm`; a native entry point reads one JSON
request from standard input and writes one JSON response to standard output
for local testing.

The site page accepts a query, schema, and newline-delimited JSON records. It
compiles and builds once, then evaluates the records in a dedicated worker and
returns matching records in their original order. The UI uses the standard
capability set and HFF's default compile and local-match limits. It does not
provide a distributable JavaScript package, SQL translation, or custom
extension registration.

## Request and response

The native command and WebAssembly bridge accept a JSON object with these
fields:

| Field | Meaning |
|:--|:--|
| `query` | HFF query source. |
| `schema` | Array of field descriptors: `name`, `type`, optional `optional`, and nested `fields` for a struct. |
| `records` | Array of JSON values to validate and match. Each is transported as raw JSON text so number spellings are preserved. |
| `without` | Optional array of standard capability IDs to remove from the default capability set. |

The native command accepts the same batch request as the browser bridge:

```sh
printf '%s' '{"query":"age>=18","schema":[{"name":"age","type":"int"}],"records":[{"age":20},{"age":12}]}' | go run ./playground
```

For a batch, a successful response has `ok: true` and `batch`, containing
`filtered` (the matching raw JSON records in input order), `total` (the number
of input records), and `timings`. The timing fields are `compile_ms`,
`build_ms`, and `evaluate_ms`. Evaluation includes validation and matching for
the whole batch; request decoding, schema setup, and worker loading are outside
these measurements. If a record fails, the response has `ok: false`, no partial
batch, and an `error` with a stage, HFF error code, message, and 1-based record
index. Source line/column and input path are included when available. A
single-record `record` request remains available to native bridge tests.

When compilation finds independent syntax or semantic errors, the failure also
includes a source-ordered `diagnostics` array. Each entry carries its HFF error
code, message, line/column, and half-open UTF-8 byte span. The top-level error
fields remain available for compatibility, while the diagnostics array exposes
all independent query locations. The site marks each reported range and can
show an insertion marker at end-of-input; it never rewrites the query. A
cancellation or limit failure stops collection and returns no partial batch.

Schema `type` strings use HFF's existing type-guard type-expression grammar;
they are not Go type names or arbitrary source fragments. Use `type: "struct"`
with nested `fields` for a struct. The bridge compiles a type descriptor and
checks that it has the expected syntax-tree shape before using it in a schema.
The bridge starts with `capabilities.Standard()`; the native request's
`without` field only removes IDs from that set and cannot add arbitrary
capabilities. The site UI keeps the standard set fixed.

The record decoder consumes JSON tokens directly. It rejects invalid UTF-8,
invalid Unicode surrogate pairs, duplicate object keys, trailing JSON values,
and numbers outside HFF's int64/float64 range. Integer spellings become HFF
int64 values and decimal or exponent spellings become float64 values. This
avoids routing records through JavaScript objects or `float64`-only JSON
decoding, which would lose large-integer precision and numeric subtype.

## Admission limits

The bridge rejects JSON input larger than 1 MiB, nesting deeper than 64 levels,
or more than 100,000 JSON values. The root value has depth zero. These limits
apply to request JSON and record JSON; HFF's own default compile and local-match
limits also apply. They bound this private bridge and do not define a general
browser API contract.

## Build and smoke-test

Build the bridge for Go's JavaScript WebAssembly target with Go 1.27.1:

```sh
GOOS=js GOARCH=wasm go build -o /tmp/hff.wasm ./playground
```

The package test builds both the native command and WebAssembly module, then
uses Node.js to compare their JSON responses for matching, non-matching,
large-integer, float, callback, compile-error, input-error, and capability
cases. If Node.js is not on `PATH`, provide its absolute path:

```sh
HFF_WASM_NODE=/absolute/path/to/node go test ./playground
```

Without Node.js the smoke test skips. The repository's generic
[`js/wasm` package test](checks.md#test-go-packages-on-webassembly) checks
package compatibility, while this smoke test exercises the bridge itself.

The site worker loads the generated WebAssembly module and passes request JSON
to the bridge without decoding records into JavaScript objects. A worker stop
or any record error produces no partial batch result.
