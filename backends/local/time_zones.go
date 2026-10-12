package local

import (
	"context"
	"sort"
	"time"
	"unicode/utf8"
)

// TimeZoneProfile supplies explicitly versioned TZif data for named zones.
// Version identifies the caller's data snapshot; HFF does not infer a version.
// Zones maps IANA-style names to TZif bytes. Aliases point directly to a Zones
// key or UTC and cannot form chains. Build copies and parses the profile;
// matching never loads host operating-system zone data.
type TimeZoneProfile struct {
	// Version is a nonempty caller-supplied identifier for this TZif snapshot.
	Version string
	// Zones maps accepted zone names to their TZif file contents.
	Zones map[string][]byte
	// Aliases maps additional names directly to a zone in Zones or to UTC.
	Aliases map[string]string
}

// WithTimeZones supplies a versioned named-zone snapshot for local Build.
// UTC and fixed offsets are built in; a nil profile is a configuration error.
func WithTimeZones(profile *TimeZoneProfile) Option {
	return Option{func(c *Config) error {
		if profile == nil {
			return configuration("nil TimeZoneProfile")
		}
		c.TimeZones = profile
		return nil
	}}
}

type zoneSnapshot struct {
	version string
	names   map[string]*time.Location
}

// TimeZoneVersion reports the version supplied to Build. Empty means only the
// built-in UTC and fixed-offset zones are available.
func (f *Filter) TimeZoneVersion() string {
	if f == nil {
		return ""
	}
	return f.timeZones.version
}

func snapshotZones(ctx context.Context, profile *TimeZoneProfile) (zoneSnapshot, error) {
	if profile == nil {
		return zoneSnapshot{}, nil
	}
	if profile.Version == "" || !utf8.ValidString(profile.Version) {
		return zoneSnapshot{}, configuration("time-zone profile requires a valid nonempty version")
	}
	out := zoneSnapshot{version: profile.Version, names: make(map[string]*time.Location)}
	keys := make([]string, 0, len(profile.Zones))
	for name := range profile.Zones {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	for _, name := range keys {
		if err := cancelled(ctx); err != nil {
			return zoneSnapshot{}, err
		}
		if !namedZone(name) {
			return zoneSnapshot{}, configuration("invalid or reserved time-zone name: " + name)
		}
		data := append([]byte(nil), profile.Zones[name]...)
		location, err := time.LoadLocationFromTZData(name, data)
		if err != nil {
			return zoneSnapshot{}, configuration("invalid TZif data for " + name + ": " + err.Error())
		}
		out.names[name] = location
	}
	keys = keys[:0]
	for alias := range profile.Aliases {
		keys = append(keys, alias)
	}
	sort.Strings(keys)
	for _, alias := range keys {
		if err := cancelled(ctx); err != nil {
			return zoneSnapshot{}, err
		}
		if !namedZone(alias) || out.names[alias] != nil {
			return zoneSnapshot{}, configuration("invalid or conflicting time-zone alias: " + alias)
		}
		target := profile.Aliases[alias]
		location := out.names[target]
		// Look up only original zone keys, never previously processed aliases.
		if _, exists := profile.Zones[target]; !exists {
			location = nil
		}
		if target == "UTC" {
			location = time.UTC
		}
		if location == nil {
			return zoneSnapshot{}, configuration("unknown time-zone alias target: " + target)
		}
		out.names[alias] = location
	}
	return out, nil
}

func namedZone(name string) bool {
	if name == "" || name == "UTC" || !utf8.ValidString(name) {
		return false
	}
	// Reserve offset-like names even if malformed, so they cannot override
	// fixed-offset validation through a profile entry.
	return name[0] != '+' && name[0] != '-'
}

func fixedZone(name string) (*time.Location, bool) {
	if len(name) != 6 || (name[0] != '+' && name[0] != '-') || name[3] != ':' {
		return nil, false
	}
	for _, i := range []int{1, 2, 4, 5} {
		if name[i] < '0' || name[i] > '9' {
			return nil, false
		}
	}
	hours, minutes := int(name[1]-'0')*10+int(name[2]-'0'), int(name[4]-'0')*10+int(name[5]-'0')
	if hours > 23 || minutes > 59 {
		return nil, false
	}
	offset := (hours*60 + minutes) * 60
	if name[0] == '-' {
		offset = -offset
	}
	return time.FixedZone(name, offset), true
}

func (z zoneSnapshot) resolve(name string) (*time.Location, bool) {
	if name == "UTC" {
		return time.UTC, true
	}
	if len(name) > 0 && (name[0] == '+' || name[0] == '-') {
		return fixedZone(name)
	}
	location, ok := z.names[name]
	return location, ok
}
