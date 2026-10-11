# Private WebAssembly Playground Bridge

`playground/` is a private bridge for the documentation-site playground, not
a supported JavaScript SDK. Its Go `main` package uses the same public engine,
schema, value, capability, and local-filter APIs as native HFF. The browser
entry point is build-tagged for `js/wasm`; a native entry point reads one JSON
request from standard input and writes one JSON response to standard output
for local testing.

The site page provides curated examples and editable query, schema, and record
JSON. It evaluates one local match in a dedicated worker, keeps the request in
the browser, and starts a fresh worker after cancellation. The UI uses the
standard capability set and HFF's default compile and local-match limits. The
bridge does not provide a distributable JavaScript package, SQL translation,
or custom extension registration.

## Request and response

The native command and WebAssembly bridge accept a JSON object with these
fields:

| Field | Meaning |
|:--|:--|
| `query` | HFF query source. |
| `schema` | Array of field descriptors: `name`, `type`, optional `optional`, and nested `fields` for a struct. |
| `record` | One JSON value to validate and match. It is transported as raw JSON text so number spellings are preserved. |
| `without` | Optional array of standard capability IDs to remove from the default capability set. |

For example, the native command can evaluate a small request:

```sh
printf '%s' '{"query":"age>=18","schema":[{"name":"age","type":"int"}],"record":{"age":20}}' | go run ./playground
```

The response is JSON. A completed evaluation has `ok: true` and a boolean
`matched`. Failure has `ok: false`, no `matched` value, and an `error` with a
stage (`request`, `configuration`, `schema`, `compile`, `build`, `input`, or
`match`), HFF error code, and message. Source line/column and input path are
included when available.

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
to the bridge without decoding the record into JavaScript objects. Its
five-second match timeout stops the worker and produces no partial match.
