package local

import (
	"context"
	"encoding/binary"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/schema"
)

// A portable TZif v1 fixture with one fixed +01:00 local-time type.
func fixedTZif() []byte {
	data := make([]byte, 54)
	copy(data, "TZif")
	binary.BigEndian.PutUint32(data[36:40], 1) // typecnt
	binary.BigEndian.PutUint32(data[40:44], 4) // charcnt
	binary.BigEndian.PutUint32(data[44:48], 3600)
	copy(data[50:], "ONE\x00")
	return data
}

func TestZoneSnapshotAndOffsets(t *testing.T) {
	data := fixedTZif()
	profile := &TimeZoneProfile{Version: "fixture-v1", Zones: map[string][]byte{"Test/One": data},
		Aliases: map[string]string{"Legacy/One": "Test/One", "Legacy/UTC": "UTC"}}
	zones, err := snapshotZones(context.Background(), profile)
	if err != nil {
		t.Fatal(err)
	}
	for i := range data {
		data[i] = 0
	}
	profile.Version = "modified"
	profile.Aliases["Legacy/One"] = "UTC"
	delete(profile.Zones, "Test/One")
	if zones.version != "fixture-v1" {
		t.Fatal("version was not snapshotted")
	}
	for _, tc := range []struct {
		name   string
		offset int
	}{
		{"UTC", 0}, {"Test/One", 3600}, {"Legacy/One", 3600}, {"Legacy/UTC", 0},
		{"+23:59", 86340}, {"-23:59", -86340}, {"+09:00", 32400}, {"-00:00", 0},
	} {
		location, ok := zones.resolve(tc.name)
		if !ok {
			t.Fatal("missing zone:", tc.name)
		}
		_, offset := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC).In(location).Zone()
		if offset != tc.offset {
			t.Fatalf("%s: got offset %d want %d", tc.name, offset, tc.offset)
		}
	}
	for _, name := range []string{"", "Local", "Asia/Tokyo", "+24:00", "-01:60", "+9:00", "+aa:00"} {
		if _, ok := zones.resolve(name); ok {
			t.Fatalf("unavailable or invalid zone accepted: %q", name)
		}
	}
}

func TestInvalidZoneProfiles(t *testing.T) {
	for _, profile := range []*TimeZoneProfile{
		{Zones: map[string][]byte{"Test/One": fixedTZif()}},
		{Version: "v", Zones: map[string][]byte{"UTC": fixedTZif()}},
		{Version: "v", Zones: map[string][]byte{"+24:00": fixedTZif()}},
		{Version: "v", Zones: map[string][]byte{"Test/One": []byte("bad")}},
		{Version: "v", Aliases: map[string]string{"A": "missing"}},
		{Version: "v", Aliases: map[string]string{"A": "B", "B": "UTC"}},
		{Version: "v", Zones: map[string][]byte{"A": fixedTZif()}, Aliases: map[string]string{"A": "UTC"}},
	} {
		_, err := snapshotZones(context.Background(), profile)
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.ConfigurationError {
			t.Fatalf("invalid profile: got %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := snapshotZones(ctx, &TimeZoneProfile{Version: "v", Zones: map[string][]byte{"Test/One": fixedTZif()}})
	if !errors.Is(err, context.Canceled) {
		t.Fatal("profile parsing must respect cancellation:", err)
	}
}

func TestFilterUsesPrivateZoneProfile(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Timestamp("created"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `created:hours("Legacy/One")=1 created:string("Legacy/One")="2026-01-01T01:00:00+01:00"`)
	if err != nil {
		t.Fatal(err)
	}
	profile := &TimeZoneProfile{Version: "fixture-v1", Zones: map[string][]byte{"Test/One": fixedTZif()}, Aliases: map[string]string{"Legacy/One": "Test/One"}}
	f, err := Build(ctx, p, WithTimeZones(profile))
	if err != nil {
		t.Fatal(err)
	}
	profile.Version = "changed"
	profile.Zones["Test/One"][44] = 255
	profile.Aliases["Legacy/One"] = "UTC"
	if f.TimeZoneVersion() != "fixture-v1" {
		t.Fatal("filter retained caller configuration")
	}
	record := hff.Object(map[string]hff.Value{"created": hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))})
	var workers sync.WaitGroup
	for range 8 {
		workers.Go(func() {
			for range 20 {
				yes, err := f.Match(ctx, record)
				if err != nil || !yes {
					t.Error("named-zone snapshot:", yes, err)
					return
				}
			}
		})
	}
	workers.Wait()
	for _, options := range [][]Option{{WithTimeZones(nil)}, {WithConfig(&Config{TimeZones: &TimeZoneProfile{Version: "v", Zones: map[string][]byte{"Bad": []byte("invalid")}}})}} {
		_, err := Build(ctx, p, options...)
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.ConfigurationError {
			t.Fatal("invalid zone configuration:", err)
		}
	}
	plain, err := Build(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if plain.TimeZoneVersion() != "" {
		t.Fatal("built-in-only filter claims a TZif version")
	}
	yes, err := plain.Match(ctx, record)
	if err != nil || yes {
		t.Fatal("unregistered named zone must not match:", yes, err)
	}
}

func TestHistoricalSecondOffsetCannotBeSerializedLossily(t *testing.T) {
	data := fixedTZif()
	binary.BigEndian.PutUint32(data[44:48], 30)
	ctx := context.Background()
	zones, err := snapshotZones(ctx, &TimeZoneProfile{Version: "historical", Zones: map[string][]byte{"Test/Seconds": data}})
	if err != nil {
		t.Fatal(err)
	}
	e := evaluation{ctx: ctx, timeZones: zones}
	value, err := e.temporal("timestamp.string", core.Timestamp(time.Date(1900, 1, 1, 0, 0, 0, 0, time.UTC)), []core.Value{core.String("Test/Seconds")})
	if err != nil || !value.present || value.value.Kind() != core.NullKind {
		t.Fatal("unrepresentable offset must not be truncated:", value, err)
	}
}
