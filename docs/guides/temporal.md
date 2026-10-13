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
