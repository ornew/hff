# 008. Versioned Time-Zone Profiles

- **Status**: Accepted
- **Author**: @ornew
- **Date**: 2026-10-11

## Summary

Named time zones use caller-supplied, versioned TZif data captured when a local
filter is built. UTC and fixed offsets remain available without zone data.
Matching uses the filter's immutable snapshot and never discovers zone files
from the machine or reads environment settings.

## Motivation

Timestamp component extraction and rendering must be deterministic across
machines and over time. Calling `time.LoadLocation` during matching can make
results depend on whichever files happen to be installed and may perform
filesystem access. Capturing the versioned data during Build lets the
application choose the data release and keeps Match pure and reusable.

## Goals

- Resolve UTC and fixed offsets without an external time-zone database.
- Let the application supply exactly the named-zone data and aliases it needs.
- Copy and parse profile data at Build so filter behavior is immutable.
- Expose the chosen data version for compatibility discovery.
- Keep named-zone matching independent of OS files and environment settings.

## Design

### Public behavior and contracts

`local.TimeZoneProfile` contains a nonempty version label, a map from zone names
to TZif byte slices, and an optional alias map. `local.WithTimeZones` and
`local.Config.TimeZones` provide the profile to `local.Build`. Build copies the
TZif bytes and parses them into private locations; mutation of the caller's
maps or buffers after Build does not change the filter. `Filter.TimeZoneVersion`
returns the supplied version label, or an empty string when the filter uses
only built-in zones.

UTC and fixed offsets from `-23:59` through `+23:59` are always available and
cannot be overridden. Named zones must exist in the supplied profile. An alias
must point directly to one of the profile's zone keys or to UTC; aliases cannot
form chains. Invalid, empty-version, conflicting, or unresolved profiles are
configuration errors. A valid query name that is absent from the profile yields
null at runtime. Profile version labels identify the data selected by the
application; HFF does not infer or verify the publisher's release name.

Timestamp component methods use UTC when their optional zone argument is
omitted. The timestamp string method requires an explicit zone. If the selected
zone's historical offset has seconds, or formatting would leave the supported
RFC 3339 year/offset range, formatting returns null rather than lose information.

### Implementation and invariants

The profile is validated and snapshotted during Build. Each zone's TZif bytes
are copied before parsing. Aliases resolve only against original zone entries or
UTC, preventing order-dependent alias chains. Match resolves names only in the
immutable snapshot plus the built-in UTC/fixed-offset paths. It performs no
zone-file discovery or environment lookup.

### Compatibility and backend support

The time-zone profile is a local-backend configuration contract. A different
backend must report its own supported zone data and cannot claim equivalent
results unless it uses a compatible version and name mapping. The version label
and supported aliases are part of an engine's portability profile.

### Testing

Tests verify copied ownership, profile version reporting, direct aliases,
reserved names, invalid TZif and alias targets, unknown-name null behavior,
concurrent reuse, and stable results from a hand-built TZif fixture without
reading host zone data.

## Alternatives considered

### Load named zones from the operating system during Match

This needs no service configuration, but makes results depend on machine state
and may perform I/O during evaluation. It is not selected.

### Capture the operating system's time-zone database during Build

Capturing system data once avoids per-match lookup but still makes the chosen
version implicit and complicates reproducing results. The caller-supplied
profile makes the version and input data explicit.

### Bundle one time-zone database in HFF

A bundled database makes defaults convenient but increases binary size and
forces the library to choose when services receive data updates. Services
instead select a versioned profile appropriate to their deployment.

## Limitations and open questions

The application supplies and labels the profile data; HFF validates its TZif
structure but does not attest to its provenance. Backends other than the local
evaluator must define their own profile and execution contracts.
