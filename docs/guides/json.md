# JSON Parsing and Serialization

The local backend supports strict JSON parsing, canonical JSON serialization,
and string conversion for every HFF value. Method signatures are listed in the
[method specification](../../spec/methods.md). JSON methods use the local
runtime limits described in the [configuration guide](configuration.md).

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
number spelling.

## Serialize values

`any:tojson()` emits compact canonical JSON. Object keys are sorted in Unicode
code-point order. Strings escape quotes, backslashes, and control characters;
other Unicode characters are emitted as UTF-8. Float64 values use a shortest
round-trip spelling that preserves the float type, so `1.0` remains `1.0` and
negative zero is `-0.0`. Bytes use standard padded base64 JSON strings;
timestamps use UTC RFC 3339 strings; durations use their canonical duration
strings. `fromjson()` does not restore these non-JSON types automatically.

`any:tostring()` leaves strings unquoted, formats scalar values directly, and
uses compact canonical JSON for lists and objects. Null receivers produce the
string `"null"` for both methods. No-value propagates without invoking the
method.

```hff
raw:fromjson:tojson="{\"a\":1,\"z\":2}"
value:tostring="{\"a\":1,\"z\":2}"
```

## Resource limits and errors

`MaxJSONDepth` defaults to 64 and counts the JSON root as depth one. It bounds
both parsing and serialization, including list/object output from
`tostring()`. Scalar `tostring()` has depth one. Exceeding the limit aborts the
match with a limit error; it does not produce null or partial output.

Parsing charges input bytes and decoded values to the local `MaxSteps` and
`MaxGeneratedValues` budgets. Serialization charges traversal and sorting to
`MaxSteps`. The JSON depth limit also prevents unbounded recursive parsing and
serialization. Cancellation and exhausted limits abort evaluation. SQL
currently rejects JSON parsing and serialization methods during `Build`.

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
