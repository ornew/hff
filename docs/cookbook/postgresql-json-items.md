# Select Tagged Values from JSON Object Entries

PostgreSQL can preserve the fixed key and value positions produced by
`object:items()` when the schema and stored JSON category identify the value
type. This recipe filters a JSONB map of nullable Bytes values by key and
decoded byte length.

```sql
CREATE TABLE artifacts (
    id bigint PRIMARY KEY,
    attachments jsonb
);

INSERT INTO artifacts VALUES
    (1, '{"release-manifest":"YQ=="}'),
    (2, '{"draft":"YQ=="}'),
    (3, '{"release-manifest":""}'),
    (4, '{"release-manifest":null}'),
    (5, 'null');
```

Declare `attachments` as `Map<Bytes|null>|Null`, bind it to the JSONB column,
and compile:

```hff
attachments:items:any(item=>item[0]:startswith("release-") item[1]:len>0)
```

Only row 1 matches: its key starts with `release-` and its decoded Bytes value
is nonempty. The `items()` callback receives `[String key, Bytes|null value]`;
the backend retains those roles through the literal tuple indexes. JSON stores
Bytes as canonical padded standard-base64 text, so `"YQ=="` represents the
single byte `a`. A stored null remains an ordinary null value and does not
match `len>0`.

Run the [example program](https://github.com/ornew/hff/tree/main/examples/sql-json-items)
from the repository root with:

```sh
go run ./examples/sql-json-items
```

It prints the generated PostgreSQL predicate and arguments, then the Local
results for the same records. Building the SQL does not connect to a database.

The backend preserves this positional information only when the source
`items()` result and schema establish the key/value roles. Constant indexes
`0` and `-2` select the String key; `1` and `-1` select the value. Dynamic
indexes that need tagged-type dispatch, same-JSON-category alternatives whose
identity cannot be recovered, and `Any` strings requiring Bytes identity
remain unsupported. Missing keys, stored null, and guard-produced no-value
retain their separate HFF behavior. See the
[PostgreSQL JSON guide](../backends/postgresql/json.md#bytes-in-json-documents)
for the supported subset and its limits.
