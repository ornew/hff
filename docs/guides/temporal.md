# Timestamps, Durations, and Time Zones

The local backend supports timestamp parsing and comparison, fixed-duration
arithmetic, component extraction, and formatting. The signatures and edge cases
are defined in the [method reference](../reference/methods.md).

## Parse, compare, and format timestamps

Timestamp strings use RFC 3339 with a numeric offset or `Z`. HFF keeps
nanosecond precision, accepts years 0000 through 9999, and rejects leap seconds,
invalid dates, seconds above 59, and fractions longer than nine digits. Invalid
or unrepresentable values yield null.

```hff
created:after("2026-10-01T00:00:00Z":timestamp)
created:hours("+09:00")=18
created:add("30m":duration):string("UTC")="2026-10-11T10:00:00Z"
```

The component methods `year()`, `month()`, `dayofmonth()`, `dayofyear()`,
`hours()`, `minutes()`, `seconds()`, and `milliseconds()` accept an optional
time-zone argument and use UTC when it is omitted. They return integer
components; month, day-of-month, and day-of-year are one-based. The
`timestamp:string(tz)` method requires an explicit zone and formats RFC 3339,
omitting trailing fractional zeros. If a historical zone offset includes
seconds or is outside the RFC 3339 range, formatting yields null rather than
changing the represented instant.

### Store PostgreSQL timestamps

Bind a schema-known Timestamp to `timestamptz` with `BindTimestamp`. PostgreSQL
stores timestamps at microsecond resolution, so use the optional
`WithNanosecondRemainder` `smallint` column when HFF nanosecond precision is
needed. The base timestamp is floored to a whole microsecond; the remainder
stores the following 0–999 nanoseconds. This representation does not round the
HFF instant during query translation. Flooring also applies before the Unix
epoch, so the remainder is always nonnegative.
The split uses exact integer arithmetic, including for instants immediately
before a second boundary, so conversion does not round the query value.

`BindTimestamp` also works as a child binding inside `BindStruct` and as a
`BindRelation` item binding. A flattened struct supports direct predicates on
the Timestamp field. A direct relation binding supports `size()` through a
correlated row count, even when its item is a Timestamp. Direct `has(index)`
also supports statically known numeric or null/no-value indexes, including
nullable numeric types; negative indexes are checked from the end without
materializing items or requiring a unique order. Relation items also support
direct Timestamp-field access in `any()` and `all()` callbacks.
Guarded or computed relation receivers and operations that need full item or
list values, such as indexing or materialization, may still fail at `Build`.
These bindings do not make a containing struct, relation item, list, or object
with a Timestamp materializable.

If the remainder column is omitted, the caller guarantees all stored values
are microsecond-exact. If it is bound, the caller guarantees both columns are
simultaneously NULL or non-NULL and the remainder is in the range 0–999. A pair
of SQL NULL values maps to HFF null, and nullable schema fields accept it. Add
constraints matching those storage requirements:

```sql
CREATE TABLE events (
    id bigint PRIMARY KEY,
    created_at timestamptz,
    created_ns_remainder smallint,
    CHECK ((created_at IS NULL) = (created_ns_remainder IS NULL)),
    CHECK (created_ns_remainder IS NULL OR created_ns_remainder BETWEEN 0 AND 999)
);
INSERT INTO events VALUES
    (101, '2026-10-01 00:00:00+00', 123),
    (102, NULL, NULL);
```

The first row represents `2026-10-01T00:00:00.000000123Z`. HFF year 0000 maps
to PostgreSQL 1 BC so the complete HFF year range remains representable. A
`timestamptz` stores an instant, not its original offset; PostgreSQL normalizes
it to UTC and displays it using the session time zone.

Declare the HFF field and bind both columns together:

```go
schema.Struct(schema.Nullable(schema.Timestamp("created_at")))

hffsql.BindTimestamp(
    hff.Path{"created_at"},
    hffsql.Column("events", "created_at"),
    hffsql.WithNanosecondRemainder(hffsql.Column("events", "created_ns_remainder")),
)
```

For example, `created_at:after("2026-10-01T00:00:00Z":timestamp)` matches
row 101 by its retained nanoseconds. Compile and build the predicate, then pass
`Query.Where()` and `Query.Args()` to the service's database call.

For native timestamp bindings, PostgreSQL currently supports direct timestamp
comparisons, `before()` and `after()`, `add(duration)`, `in()`, `exists()`, and
`type()`. The component methods `year()`, `month()`, `dayofmonth()`,
`dayofyear()`, `hours()`, `minutes()`, `seconds()`, and `milliseconds()` use
UTC and take no zone argument. `string()` and `json()` also format in UTC.
The `string:timestamp` conversion is accepted only for a literal RFC 3339
string; parsing data-dependent strings and zone arguments are unsupported by
this backend. `Build` rejects operations outside this subset. Timestamp
storage in JSON or JSONB remains unsupported.

The [timestamp binding example](https://github.com/ornew/hff/tree/main/examples/sql-timestamps)
builds this kind of predicate and prints whether the predicate was built and
how many parameters it produced; it does not print the SQL fragment or connect
to a database. From the repository root, run:

```sh
go run ./examples/sql-timestamps
```

For example, a flattened profile can expose a nested Timestamp to a predicate
without constructing the complete profile value:

```sql
CREATE TABLE records (
    id bigint PRIMARY KEY,
    profile_present boolean NOT NULL,
    profile_null boolean NOT NULL,
    created_at timestamptz,
    created_ns_remainder smallint,
    CHECK (NOT profile_null OR profile_present),
    CHECK ((created_at IS NULL) = (created_ns_remainder IS NULL)),
    CHECK (created_ns_remainder IS NULL OR created_ns_remainder BETWEEN 0 AND 999)
);
```

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/ornew/hff"
    hffsql "github.com/ornew/hff/backends/sql"
    "github.com/ornew/hff/capabilities"
    "github.com/ornew/hff/engine"
    "github.com/ornew/hff/schema"
)

func main() {
    ctx := context.Background()
    e, err := engine.New(
        engine.WithSchema(schema.Struct(schema.Nullable(schema.Field(
            "profile", schema.Struct(schema.Timestamp("created")),
        )))),
        engine.WithCapabilities(capabilities.Standard()),
    )
    if err != nil {
        log.Fatal(err)
    }

    profile := hffsql.BindStruct(hff.Path{"profile"},
        hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_present"), schema.BoolType)),
        hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_null"), schema.BoolType)),
        hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("records", "created_at"),
            hffsql.WithNanosecondRemainder(hffsql.Column("records", "created_ns_remainder"))),
    )
    program, err := e.Compile(ctx,
        `profile.created:after("2026-10-01T00:00:00Z":timestamp)`,
    )
    if err != nil {
        log.Fatal(err)
    }
    query, err := hffsql.Build(ctx, program,
        hffsql.WithDialect(hffsql.PostgreSQL),
        hffsql.WithBindings(profile),
    )
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("WHERE", query.Where(), "Args", query.Args())
}
```

For a relation item, use the direct Timestamp field in an `any()` or `all()`
callback. For example,
`events:any(event=>event.created:after("2026-10-01T00:00:00Z":timestamp))`
filters related rows. A direct `events:size` query counts correlated rows
without constructing Timestamp items. For example, `events:has(-1)` checks
whether the relation contains an item at its final negative index. Direct
`has(index)` supports statically known numeric or null/no-value indexes,
including nullable numeric types. Genuinely dynamic `Any` or mixed-union
indexes use the JSON fallback and may be rejected when the relation has
Timestamp items. Indexing or operations on guarded or computed relation
receivers may still fail to build when their SQL lowering requires complete
item or list values.

## Parse and measure durations

Duration strings combine signed decimal components with `h`, `m`, `s`, `ms`,
`us`, or `ns` suffixes. For example, `"1h30m"`, `"-23.4s"`, `".5h"`, and
`"1h34us"` are valid. Components are summed exactly before checking that the
result is an integral number of nanoseconds in the signed int64 range. Invalid,
sub-nanosecond, or overflowing values yield null. Days and weeks are not
calendar-free units and are not accepted.

Duration unit methods return total units: `nanoseconds()` returns int64, while
hours, minutes, seconds, milliseconds, and microseconds return float64. Adding
a duration to a timestamp uses fixed elapsed time, not calendar arithmetic; an
out-of-range result yields null.

### Store native PostgreSQL durations

Bind a schema-known Duration column to a PostgreSQL `bigint` containing signed
int64 nanoseconds. The binding does not round or reinterpret the value; SQL
`NULL` is HFF null. A nullable schema field accepts SQL `NULL`, while a
non-nullable field requires a non-null column value.

```sql
CREATE TABLE job_runs (
    id bigint PRIMARY KEY,
    elapsed_ns bigint
);
INSERT INTO job_runs VALUES
    (1, 45000000000),
    (2, 90000000000),
    (3, NULL);
```

Declare the nullable field as a Duration and bind it to the nanosecond column:

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/ornew/hff"
    hffsql "github.com/ornew/hff/backends/sql"
    "github.com/ornew/hff/capabilities"
    "github.com/ornew/hff/engine"
    "github.com/ornew/hff/schema"
)

func main() {
    ctx := context.Background()
    e, err := engine.New(
        engine.WithSchema(schema.Struct(
            schema.Nullable(schema.Duration("elapsed")),
        )),
        engine.WithCapabilities(capabilities.Standard()),
    )
    if err != nil {
        log.Fatal(err)
    }
    program, err := e.Compile(ctx, `elapsed:seconds>=60`)
    if err != nil {
        log.Fatal(err)
    }
    query, err := hffsql.Build(ctx, program,
        hffsql.WithDialect(hffsql.PostgreSQL),
        hffsql.WithBindings(hffsql.Bind(
            hff.Path{"elapsed"},
            hffsql.Column("job_runs", "elapsed_ns"),
        )),
    )
    if err != nil {
        log.Fatal(err)
    }
    fmt.Println("Predicate built:", query.Where() != "")
    fmt.Println("Parameter count:", len(query.Args()))
}
```

Executing the predicate against the sample table selects row 2; the runnable
example demonstrates construction and does not execute the query. Pass
`query.Where()` and `query.Args()` to the application's PostgreSQL driver.
Native Duration columns support Duration unit methods, comparisons, truthiness,
membership, and canonical `string()`/`json()` output. A Duration column can
also be used as a typed relation ordering key, for example with
`hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("job_runs", "elapsed_ns"), schema.DurationType))`.
The same scalar mapping works for a direct flattened `BindStruct` field, but
materializing a whole struct containing Duration remains unsupported.
JSON/JSONB and native composite Duration storage are also unavailable. The
[runnable stored-duration example](https://github.com/ornew/hff/tree/main/examples/sql-stored-durations)
shows the complete binding setup and can be run with
`go run ./examples/sql-stored-durations` from the repository root.

#### Filter a relation of Duration values

A direct scalar relation item can also bind a `bigint` nanosecond column. Make
the item nullable when the child column may be SQL `NULL`:

```sql
CREATE TABLE jobs (
    id bigint PRIMARY KEY
);
CREATE TABLE job_durations (
    id bigint PRIMARY KEY,
    job_id bigint NOT NULL REFERENCES jobs(id),
    elapsed_ns bigint
);
INSERT INTO jobs VALUES (41), (42);
INSERT INTO job_durations VALUES
    (1, 41, 45000000000),
    (2, 41, 90000000000),
    (3, 41, NULL);
```

Declare the list item as nullable Duration and bind the child column relative
to each relation row. The following is a fragment inside a function returning
`error`; it assumes the HFF, SQL backend, and schema imports. Use
`recordSchema` when constructing the engine, then attach `durationRelation`
with `BindRelation` during PostgreSQL `Build`.

```go
durationItems := schema.NullableType(schema.DurationType)
recordSchema := schema.Struct(schema.List("durations", durationItems))

durationRelation, err := hffsql.NewRelation(
    hffsql.WithRelationSource(hffsql.Column("job_durations"), "duration"),
    hffsql.WithCorrelation(hffsql.Equal(
        hffsql.ColumnValue(hffsql.Column("duration", "job_id"), schema.IntType),
        hffsql.ColumnValue(hffsql.Column("jobs", "id"), schema.IntType),
    )),
    hffsql.WithRelationItem(durationItems,
        hffsql.Bind(hff.Path{}, hffsql.Column("duration", "elapsed_ns")),
    ),
)
if err != nil {
    return err
}
```

Either callback predicate can be compiled and attached with
`BindRelation(hff.Path{"durations"}, durationRelation)`:

```hff
durations:any(d=>d:seconds>=60)
durations:all(d=>d:exists)
```

The relation also supports `size()` and `has()` without an item ordering:

```hff
durations:size=3
durations:has(-1)
```

`size()` counts the matching child rows, including a row whose bound Duration
is SQL `NULL`. `has()` checks whether a statically known numeric index exists;
nullable numeric, null, and no-value indexes are also supported. A dynamic
`Any` or mixed-union index may require the JSON fallback, which can fail to
build if it would need to materialize the unsupported Duration list.

The first predicate matches job 41 because one item is 90 seconds. A SQL-null
item remains HFF null; its `seconds()` comparison is null and does not make
`any()` true. The second predicate is false for job 41 because of its null item,
and true for job 42's empty relation. Plain `any()` and `all()` do not require a
unique item order. They do not imply that the complete Duration list can be
materialized or used by every order-sensitive operation; those shapes may
still be rejected at `Build`.

### PostgreSQL computed durations

The PostgreSQL backend can parse a text column into a computed Duration and
use its unit methods in a predicate. For example,
`elapsed:duration:seconds>=60` accepts a text value of at least one minute.
The [SQL duration example](https://github.com/ornew/hff/tree/main/examples/sql-durations)
shows the schema, binding, and parameterized predicate construction. It builds
the query without connecting to a database. Run it from the repository root
with `go run ./examples/sql-durations`; it prints that the predicate was built
and reports one bound parameter. This text conversion is separate from the
native `bigint` Duration binding described above. JSON and JSONB Duration
materialization remains unavailable. The backend does not enforce the local
parser's `MaxSteps`
budget, so database timeouts and input-size controls remain the caller's
responsibility.

## Supply versioned time-zone data

UTC and fixed offsets such as `+09:00` work without a profile. Named IANA or
Joda-Time zones come from application-supplied TZif data, so matching never
loads zone files or reads environment settings. The application chooses and
reports the profile version:

```go
profile := &local.TimeZoneProfile{
    Version: "tzdata-2026a",
    Zones:   map[string][]byte{"America/New_York": newYorkTZif},
    Aliases: map[string]string{"US/Eastern": "America/New_York"},
}
filter, err := local.Build(ctx, program, local.WithTimeZones(profile))
if err != nil {
    return err
}
fmt.Println(filter.TimeZoneVersion())
```

Supply TZif bytes from an application-managed, versioned data source. `Build`
copies and parses the profile into the filter, so later changes to caller-owned
maps or byte slices do not alter its behavior. Aliases point directly to a zone
in `Zones` or to `UTC`; alias chains and attempts to override `UTC` or fixed
offset names are rejected. A nil, invalid, or unversioned profile is a
configuration error. A name not available in the profile yields null when used
in a query. `TimeZoneVersion()` returns an empty string if the filter uses only
built-in UTC and fixed-offset zones.

## Run the example

```sh
go run ./examples/temporal
```

Expected output:

```text
created:after("2026-10-01T00:00:00Z":timestamp) -> true
created:hours("+09:00")=18 elapsed:duration:minutes>=60 -> true
created:add("30m":duration):string("UTC")="2026-10-11T10:00:00Z" -> true
```

The parser charges timestamp and zone-name input length to the local per-match
`MaxSteps` budget. Duration parsing reserves a conservative cost based on the
input length before allocating exact decimal intermediates. See the
[local backend configuration](../backends/local/configuration.md) for local runtime limits.
