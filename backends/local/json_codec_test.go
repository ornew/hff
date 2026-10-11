package local

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff/internal/core"
)

func TestCanonicalJSONKernels(t *testing.T) {
	for _, tc := range []struct{ input, output string }{
		{` { "b":2, "a":1.0 } `, `{"a":1.0,"b":2}`},
		{`[null,true,false,"a",-0.0,1e0]`, `[null,true,false,"a",-0.0,1.0]`},
		{`"\ud83d\ude00"`, `"😀"`},
		{`"<>&/\u2028\u2029"`, `"<>&/` + "\u2028\u2029" + `"`},
		{`{"\u0061":1,"b":[]}`, `{"a":1,"b":[]}`},
	} {
		e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		decoded, err := e.decodeJSON(tc.input, 64)
		if err != nil {
			t.Fatal(err)
		}
		encoded, err := e.encodeJSON(decoded.value, 64)
		text, _ := encoded.value.AsString()
		if err != nil || text != tc.output {
			t.Fatalf("%s: got %q %v want %q", tc.input, text, err, tc.output)
		}
		if !json.Valid([]byte(text)) {
			t.Fatal("output is not JSON:", text)
		}
	}
	value := core.OwnedObject(map[string]core.Value{
		"bytes": core.Bytes([]byte{0, 255}), "duration": core.Duration(-time.Nanosecond),
		"timestamp": core.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 120000000, time.UTC)),
	})
	e := evaluation{ctx: context.Background()}
	encoded, err := e.encodeJSON(value, 64)
	text, _ := encoded.value.AsString()
	if err != nil || text != `{"bytes":"AP8=","duration":"-0.000000001s","timestamp":"2026-01-01T00:00:00.12Z"}` {
		t.Fatal(text, err)
	}
}

func TestStrictJSONKernels(t *testing.T) {
	for _, text := range []string{
		`{"a":1,"\u0061":2}`, `{"x":{"a":1,"a":2}}`,
		`"\ud800"`, `"\udc00"`, `"\ud800\u0041"`, "\"\xff\"",
		`[1,]`, `{"a":1,}`, `01`, `1e999`, `9223372036854775808`,
		`true false`, `[NaN]`, `[Infinity]`, `+1`, ``,
	} {
		e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		result, err := e.decodeJSON(text, 64)
		if err != nil || result.value.Kind() != core.NullKind {
			t.Fatalf("invalid JSON %q: %v %v", text, result, err)
		}
	}
	e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
	r, err := e.decodeJSON(`[-0.0,1.0,1,1e-9999]`, 64)
	if err != nil {
		t.Fatal(err)
	}
	items, _ := r.value.ListView()
	f, _ := items[0].AsFloat()
	if !math.Signbit(f) || items[1].Kind() != core.FloatKind || items[2].Kind() != core.IntKind || items[3].Kind() != core.FloatKind {
		t.Fatal("numeric subtypes or signed zero lost:", items)
	}
}

func TestJSONKernelBudgets(t *testing.T) {
	for _, operation := range []func(*evaluation) (result, error){
		func(e *evaluation) (result, error) { return e.decodeJSON(`[[1]]`, 2) },
		func(e *evaluation) (result, error) { return e.encodeJSON(core.List(core.List(core.Int(1))), 2) },
		func(e *evaluation) (result, error) {
			e.limits.MaxGeneratedValues = 3
			return e.decodeJSON(`{"a":[1,null]}`, 64)
		},
		func(e *evaluation) (result, error) {
			e.limits.MaxSteps = 20
			return e.encodeJSON(core.String("\x00\x00\x00\x00"), 64)
		},
	} {
		e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		_, err := operation(&e)
		failure, ok := err.(*core.Error)
		if !ok || failure.Code != core.LimitError {
			t.Fatal("resource ceiling must abort:", err)
		}
	}
}

func TestDurationTextRoundTrip(t *testing.T) {
	for _, d := range []time.Duration{0, 1, -1, time.Hour, time.Hour + time.Minute + 123456789, time.Duration(math.MinInt64), time.Duration(math.MaxInt64)} {
		text := durationText(d)
		got, ok := parseDuration(text).AsDuration()
		if !ok || got != d {
			t.Fatal(d, text, got)
		}
	}
}

func FuzzJSONRoundTrip(f *testing.F) {
	for _, seed := range []string{`null`, `{"x":[1,-0.0,true]}`, `"\ud83d\ude00"`, `{"a":1,"a":2}`, `[1,]`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 512 {
			t.Skip()
		}
		e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
		decoded, err := e.decodeJSON(text, 32)
		if err != nil {
			return
		}
		encoded, err := e.encodeJSON(decoded.value, 32)
		if err != nil {
			t.Fatal(err)
		}
		output, _ := encoded.value.AsString()
		if !json.Valid([]byte(output)) {
			t.Fatalf("invalid serialization: %q", output)
		}
		again, err := e.decodeJSON(output, 32)
		if err != nil {
			t.Fatal(err)
		}
		equal, err := e.equal(decoded.value, again.value)
		if err != nil || !equal {
			t.Fatalf("round-trip value changed: %q %q %v", text, output, err)
		}
	})
}

// Cancel during a scan, rather than before the decoder begins.
type jsonScanContext struct {
	context.Context
	calls  int
	at     int
	cancel context.CancelCauseFunc
	cause  error
}

func (c *jsonScanContext) Err() error {
	c.calls++
	at := c.at
	if at == 0 {
		at = 5
	}
	if c.calls == at {
		c.cancel(c.cause)
	}
	return c.Context.Err()
}

func TestJSONCancellationDuringTokenScans(t *testing.T) {
	long := strings.Repeat("a", 32768)
	for _, tc := range []struct{ name, text string }{
		{"leading whitespace", strings.Repeat(" ", 32768) + "null"},
		{"trailing whitespace", "null" + strings.Repeat(" ", 32768)},
		{"string", `"` + long + `"`},
		{"object key", `{"` + long + `":true}`},
		{"number", strings.Repeat("1", 32768)},
		{"escaped string", `"` + strings.Repeat(`\n`, 16384) + `"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("request stopped during JSON scan")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			probe := &jsonScanContext{Context: ctx, cancel: cancel, cause: cause}
			e := evaluation{ctx: probe, limits: core.DefaultLimits()}
			got, err := e.decodeJSON(tc.text, 64)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if got.present || got.value.Kind() != core.InvalidKind {
				t.Fatalf("partial value returned: %#v", got)
			}
		})
	}
}

func TestJSONScannerStopsBeforeTokenEnd(t *testing.T) {
	for _, kind := range []string{"whitespace", "string", "number"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			probe := &jsonScanContext{Context: ctx, cancel: cancel, cause: errors.New("scan stopped")}
			e := evaluation{ctx: probe, limits: core.DefaultLimits()}
			r := jsonReader{e: &e, maxDepth: 64}
			switch kind {
			case "whitespace":
				r.text = strings.Repeat(" ", 32768)
				r.whitespace()
			case "string":
				r.text = `"` + strings.Repeat("a", 32768) + `"`
				r.quoted()
			case "number":
				r.text = strings.Repeat("1", 32768)
				_, r.err = r.value(1)
			}
			if !errors.Is(r.err, context.Canceled) || r.pos < 4096 || r.pos >= len(r.text) {
				t.Fatalf("scanner did not stop within token: pos=%d length=%d error=%v", r.pos, len(r.text), r.err)
			}
		})
	}
}

func TestJSONCancellationDuringOutputScans(t *testing.T) {
	for _, tc := range []struct {
		name string
		at   int
	}{{"escaped length", 5}, {"quoted output", 14}} {
		t.Run(tc.name, func(t *testing.T) {
			cause := errors.New("JSON output stopped")
			ctx, cancel := context.WithCancelCause(context.Background())
			defer cancel(nil)
			probe := &jsonScanContext{Context: ctx, cancel: cancel, cause: cause, at: tc.at}
			e := evaluation{ctx: probe, limits: core.DefaultLimits()}
			got, err := e.encodeJSON(core.String(strings.Repeat("a", 32768)), 64)
			if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) {
				t.Fatalf("cancellation lost: %v", err)
			}
			if got.present || got.value.Kind() != core.InvalidKind {
				t.Fatalf("partial output: %#v", got)
			}
			// Distinguish cancellation before output reservation from the quoting pass.
			wantSteps := 1 + 32768
			if tc.name == "quoted output" {
				wantSteps += 32768 + 2
			}
			if e.steps != wantSteps {
				t.Fatalf("wrong scan phase: steps=%d want=%d", e.steps, wantSteps)
			}
		})
	}
}

func TestJSONQuotedOutputBudget(t *testing.T) {
	text := strings.Repeat("a\n\x00", 3000)
	e := evaluation{ctx: context.Background(), limits: core.DefaultLimits()}
	got, err := e.encodeJSON(core.String(text), 64)
	output, _ := got.value.AsString()
	if err != nil {
		t.Fatal(err)
	}
	// One value visit, input bytes, and escaped output bytes; checkpoints add no steps.
	if e.steps != 1+len(text)+len(output) {
		t.Fatalf("byte work counted twice: %d", e.steps)
	}
}
