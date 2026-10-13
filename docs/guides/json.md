# JSON Parsing and Serialization

The local backend supports strict JSON parsing, canonical JSON serialization,
and string conversion for every HFF value. Method signatures are listed in the
[method reference](../reference/methods.md). JSON methods use the local
runtime limits described in the [local backend configuration](../backends/local/configuration.md).

## Parse JSON

`string:fromjson()` returns an HFF value. JSON strings, booleans, nulls,
arrays, and objects retain their corresponding HFF types. Integer spellings
that fit int64 become integers; decimal or exponent spellings become float64,
including `-0.0`. Bytes, timestamps, and durations parsed from JSON are
ordinary strings; `fromjson()` does not infer those non-JSON types.

```hff
raw:fromjson:[object]:get("name")="Ada"
raw:fromjson:[list[string]]:contains("admin")
```

The parser rejects duplicate object keys, invalid UTF-8 and surrogate escapes,
trailing data, and numbers outside the supported numeric range. Invalid input
returns null. The parser preserves the int64/float64 distinction from the JSON
number spelling. The local backend uses Go Float parsing: finite subnormals and
underflow to signed zero are valid, while overflow is invalid. Other backends
may report different Float parsing behavior in their profiles.

## Serialize values

`any:json()` emits compact canonical JSON. Object keys are sorted in Unicode
code-point order. Strings escape quotes, backslashes, and control characters;
other Unicode characters are emitted as UTF-8. The local backend formats
Float64 values with Go's shortest round-trip spelling and keeps a decimal or
exponent marker so `1.0` remains a Float; negative zero is `-0.0`. Other
backends may use different Float text as described by their profiles. Bytes
use standard padded base64 JSON strings;
timestamps use UTC RFC 3339 strings; durations use their canonical duration
strings. `fromjson()` does not restore these non-JSON types automatically.

`any:string()` leaves strings unquoted, formats scalar values directly, and
uses compact canonical JSON for lists and objects. Null receivers produce the
string `"null"` for both methods. No-value propagates without invoking the
method.

```hff
raw:fromjson:json="{\"a\":1,\"z\":2}"
value:string="{\"a\":1,\"z\":2}"
```

## Resource limits and errors

`MaxJSONDepth` defaults to 64 and counts the JSON root as depth one. It bounds
both parsing and serialization, including list/object output from
`string()`. Scalar `string()` has depth one. Exceeding the limit aborts the
match with a limit error; it does not produce null or partial output.

Parsing charges input bytes and decoded values to the local `MaxSteps` and
`MaxGeneratedValues` budgets. Serialization charges traversal and sorting to
`MaxSteps`. The JSON depth limit also prevents unbounded recursive parsing and
serialization. Cancellation and exhausted limits abort evaluation. This guide
describes the local backend; PostgreSQL supports a separate subset of JSON
parsing and serialization operations, documented in the
[PostgreSQL JSON guide](../backends/postgresql/json.md).

## Run the example

```sh
go run ./examples/json
```

Expected output:

```text
Parsed object: true
Canonical JSON: true
Invalid JSON: false
Object string: true
```
