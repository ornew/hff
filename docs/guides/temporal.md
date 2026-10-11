# Timestamps, Durations, and Time Zones

The local backend supports timestamp parsing and comparison, fixed-duration
arithmetic, component extraction, and formatting. The signatures and edge cases
are defined in the [method specification](../../spec/methods.md).

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
[configuration guide](configuration.md) for local runtime limits.
