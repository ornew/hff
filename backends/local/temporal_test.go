package local_test

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestTemporalMethods(t *testing.T) {
	stamp := time.Date(2024, 2, 29, 23, 58, 57, 123456789, time.UTC)
	for _, tc := range []struct {
		query string
		x, y  hff.Value
		want  bool
	}{
		{`x:year=2024 x:month=2 x:dayofmonth=29 x:dayofyear=60`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:hours=23 x:minutes=58 x:seconds=57 x:milliseconds=123`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:dayofmonth("+01:00")=1 x:month("+01:00")=3 x:hours("+01:00")=0`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:string="2024-02-29T23:58:57.123456789Z"`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:string()="2024-02-29T23:58:57.123456789Z"`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:string="null"`, hff.Null(), hff.Null(), true},
		{`x:string("UTC")=null`, hff.Null(), hff.Null(), true},
		{`x:string(y:[string])=null`, hff.Null(), hff.Int(1), true},
		{`x:string("UTC")="2024-02-29T23:58:57.123456789Z"`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:string("+01:00")="2024-03-01T00:58:57.123456789+01:00"`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:hours("Asia/Tokyo")=null`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:hours("+24:00")=null`, hff.Timestamp(stamp), hff.Null(), true},
		{`x:before(y) x:after(y)`, hff.Timestamp(stamp), hff.Timestamp(stamp.Add(time.Second)), false},
		{`x:before(y) !x:after(y)`, hff.Timestamp(stamp), hff.Timestamp(stamp.Add(time.Second)), true},
		{`x:add(y):dayofmonth=1`, hff.Timestamp(stamp), hff.Duration(time.Minute * 2), true},
		{`x:add(y)=null`, hff.Timestamp(time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)), hff.Duration(1), true},
		{`x:add(y)=null`, hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), hff.Duration(-1), true},
		{`x:string("-01:00")=null`, hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), hff.Null(), true},
		{`x:hours=1.5 x:minutes=90 x:seconds=5400 x:milliseconds=5400000 x:microseconds=5400000000 x:nanoseconds=5400000000000`, hff.Duration(time.Minute * 90), hff.Null(), true},
		{`x:nanoseconds=-9223372036854775808`, hff.Duration(time.Duration(math.MinInt64)), hff.Null(), true},
		{`x:hours:type="number" x:nanoseconds:type="number"`, hff.Duration(time.Second), hff.Null(), true},
		{`x:timestamp=y`, hff.String("2024-03-01T00:58:57.123456789+01:00"), hff.Timestamp(stamp), true},
		{`x:timestamp:year=0`, hff.String("0000-01-01T00:00:00Z"), hff.Null(), true},
		{`x:duration:nanoseconds=3600000034000`, hff.String("1h34us"), hff.Null(), true},
		{`x:duration:nanoseconds=-23400000000`, hff.String("-23.4s"), hff.Null(), true},
		{`x:duration:minutes=90`, hff.String("+1h30m"), hff.Null(), true},
		{`x:duration:minutes=30`, hff.String(".5h"), hff.Null(), true},
		{`x:duration:nanoseconds=1`, hff.String(".5ns.5ns"), hff.Null(), true},
		{`x:duration:nanoseconds=-9223372036854775808`, hff.String("-9223372036854775808ns"), hff.Null(), true},
		{`x:duration:nanoseconds=9223372036854775807`, hff.String("9223372036854775807ns"), hff.Null(), true},
		{`x:duration:nanoseconds=0`, hff.String("0"), hff.Null(), true},
		{`x:timestamp=null x:duration=null`, hff.Null(), hff.Null(), true},
		{`x:year=null x:hours=null`, hff.Null(), hff.Null(), true},
	} {
		t.Run(tc.query, func(t *testing.T) {
			got, err := build(t, tc.query).Match(context.Background(), record(tc.x, tc.y))
			if err != nil || got != tc.want {
				t.Fatalf("got %v %v want %v", got, err, tc.want)
			}
		})
	}
}

func TestInvalidTemporalConversions(t *testing.T) {
	for _, text := range []string{"", "2024-02-30T00:00:00Z", "2024-02-29T00:00:60Z", "2024-02-29T24:00:00Z",
		"2024-02-29t00:00:00Z", "2024-02-29T00:00:00z", "2024-02-29T00:00:00.1234567890Z",
		"2024-02-29T00:00:00,1Z", "2024-02-29T00:00:00+24:00", "2024-02-29T00:00:00+00:60",
		"2024-02-29T00:00:00.Z", "2024-02-29T00:00:00", "0000-01-01T00:00:00+01:00", "9999-12-31T23:59:59-01:00"} {
		got, err := build(t, `x:timestamp=null`).Match(context.Background(), record(hff.String(text), hff.Null()))
		if err != nil || !got {
			t.Fatalf("invalid timestamp %q: %v %v", text, got, err)
		}
	}
	for _, text := range []string{"", "1", "1d", "1w", "1µs", "1e3s", "1h-1m", "1h 1m", "1ss", ".s", "0.1ns",
		"9223372036854775808ns", "-9223372036854775809ns", "9223372036854775807ns1ns", "+", "--1s"} {
		got, err := build(t, `x:duration=null`).Match(context.Background(), record(hff.String(text), hff.Null()))
		if err != nil || !got {
			t.Fatalf("invalid duration %q: %v %v", text, got, err)
		}
	}
	bounded := build(t, `x:duration`, local.WithLimits(hff.Limits{MaxSteps: 100}))
	_, err := bounded.Match(context.Background(), record(hff.String(strings.Repeat("0", 100)+"ns"), hff.Null()))
	code(t, err, hff.LimitError)
}
