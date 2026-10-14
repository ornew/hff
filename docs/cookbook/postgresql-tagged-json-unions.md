# Use Tagged Values in PostgreSQL JSON Unions

PostgreSQL can recover some schema-known Bytes, Duration, and Timestamp values
from JSON strings when the declared union alternatives have distinct JSON
categories. For example, a Duration or list of nullable Durations can be
selected from a JSONB column because a string and an array have different JSON
categories.

## Runnable example

The [container-union example](https://github.com/ornew/hff/tree/main/examples/sql-json-container-unions)
models this table and its optional `windows` field:

```sql
CREATE TABLE jobs (id bigint PRIMARY KEY, wait_windows jsonb);
INSERT INTO jobs VALUES
    (1, '"1s"'),
    (2, '"6m"'),
    (3, '["1s",null,"6m"]'),
    (4, '[]'),
    (5, 'null');
```

The declared HFF type is `Duration|List<Duration|null>|Null`. The program
binds `windows` to `jobs.wait_windows` as JSONB and compiles this query:

```hff
windows:[duration]>"5m":duration | windows:[list[duration|null]]:any(x=>x>"5m":duration)
```

Run it from the repository root:

```sh
go run ./examples/sql-json-container-unions
```

The program prints the generated `WHERE` clause and ordered arguments, then
shows Local results for the five stored values above. Rows 2 and 3 match; the
single short Duration, empty list, and null value do not. It does not connect
to PostgreSQL. The SQL shape uses JSON categories to distinguish a root
Duration string from a Duration list. The declared element schema identifies
the meaning of strings inside the list.

## Where dispatch is available

The same rule applies when one schema-known Bytes, Duration, or Timestamp
alternative shares a union with a List, Map, or Struct alternative and native
scalar alternatives, including nested collection values. Dispatch is available
when the schema and stored JSON category uniquely identify the active HFF type
for the operation. A shape-only operation may still work when it does not
need to identify a nested member; an operation that needs an unrecoverable
member type is rejected by `Build`.

Missing object keys, stored JSON null, and guard-produced no-value remain
distinct. In a separate schema where `tokens` is a string-keyed map and `key`
has type `Bytes|Int|Null`, a Bytes JSON string does not gain String map-key
behavior: `tokens:has(key)` returns null. Likewise, `"api":contains(key)`
returns null instead of treating that Bytes value as a String argument. If an
earlier nullable argument is invalid, later guarded arguments are not evaluated.

Same-category alternatives such as `String|Duration` remain ambiguous when
the operation requires their HFF identity. `Any` strings remain Strings.
Mixed `Int|Float` values require native JSON or preserved JSONB numeric
metadata when an operation observes the distinction or a Float representation
that JSONB erased. JSONB metadata version 1 is numeric-only; it does not add
tags for Bytes, Duration, or Timestamp. See the
[PostgreSQL JSON and JSONB guide](../backends/postgresql/json.md#tagged-scalar-alternatives-with-containers)
for the supported subset and its limits. The
[scalar JSON-union example](https://github.com/ornew/hff/tree/main/examples/sql-json-unions)
shows a separate Timestamp/Duration case with numeric metadata.
