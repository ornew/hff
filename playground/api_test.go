package main

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/ornew/hff"
)

func TestBridgeMatch(t *testing.T) {
	for _, tc := range []struct {
		name, request string
		matched       bool
		stage         string
	}{
		{"match", `{"query":"age>=18 name","schema":[{"name":"age","type":"int"},{"name":"name","type":"string"}],"record":{"age":20,"name":"Alice"}}`, true, ""},
		{"no match", `{"query":"age>=18","schema":[{"name":"age","type":"int"}],"record":{"age":12}}`, false, ""},
		{"large integer", `{"query":"n=9007199254740993","schema":[{"name":"n","type":"int"}],"record":{"n":9007199254740993}}`, true, ""},
		{"float subtype", `{"query":"n:[float]:exists","schema":[{"name":"n","type":"int|float"}],"record":{"n":1.0}}`, true, ""},
		{"optional", `{"query":"name=null","schema":[{"name":"name","type":"string","optional":true}],"record":{}}`, true, ""},
		{"nested", `{"query":"user.name","schema":[{"name":"user","type":"struct","fields":[{"name":"name","type":"string"}]}],"record":{"user":{"name":"Alice"}}}`, true, ""},
		{"syntax", `{"query":"age>","schema":[{"name":"age","type":"int"}],"record":{"age":20}}`, false, "compile"},
		{"type mismatch", `{"query":"age","schema":[{"name":"age","type":"int"}],"record":{"age":"20"}}`, false, "match"},
		{"capability", `{"query":"age>18","schema":[{"name":"age","type":"int"}],"record":{"age":20},"without":["operator:comparison"]}`, false, "compile"},
		{"invalid capability", `{"query":"age","schema":[{"name":"age","type":"int"}],"record":{"age":20},"without":["unknown"]}`, false, "configuration"},
		{"duplicate", `{"query":"age","schema":[{"name":"age","type":"int"}],"record":{"age":1,"age":2}}`, false, "request"},
		{"unknown field", `{"query":"age","oops":true}`, false, "request"},
		{"descriptor injection", `{"query":"x","schema":[{"name":"x","type":"int]:exists | x:[string"}],"record":{"x":1}}`, false, "schema"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r response
			if err := json.Unmarshal(handle(context.Background(), []byte(tc.request)), &r); err != nil {
				t.Fatal(err)
			}
			if tc.stage != "" {
				if r.OK || r.Matched != nil || r.Error == nil || r.Error.Stage != tc.stage {
					t.Fatalf("response: %+v", r)
				}
				return
			}
			if !r.OK || r.Error != nil || r.Matched == nil || *r.Matched != tc.matched {
				t.Fatalf("response: %+v", r)
			}
		})
	}
}

func TestJSONInputFidelity(t *testing.T) {
	v, err := decode(context.Background(), []byte(`[9007199254740993,1.0,-0.0]`))
	if err != nil {
		t.Fatal(err)
	}
	x, _ := v.AsList()
	i, ok := x[0].AsInt()
	if !ok || i != 9007199254740993 {
		t.Fatal(x[0])
	}
	f, ok := x[1].AsFloat()
	if !ok || f != 1 {
		t.Fatal(x[1])
	}
	f, ok = x[2].AsFloat()
	if !ok || !math.Signbit(f) {
		t.Fatal("lost negative zero")
	}
	for _, raw := range []string{`"\ud800"`, `{"\udfff":1}`, `9223372036854775808`, `1e999`, `{} {}`, `{"a":1,"\u0061":2}`, `[1,]`, `{"a":}`, string([]byte{34, 255, 34})} {
		if _, err := decode(context.Background(), []byte(raw)); err == nil {
			t.Errorf("accepted %q", raw)
		}
	}
	if _, err := decode(context.Background(), []byte(`"\ud83d\ude00"`)); err != nil {
		t.Fatal(err)
	}
}
func TestJSONInputLimitsAndCancellation(t *testing.T) {
	for _, raw := range []string{strings.Repeat(" ", maxRequestBytes+1), strings.Repeat("[", 66) + "0" + strings.Repeat("]", 66), "[" + strings.Repeat("0,", 100000) + "0]"} {
		_, err := decode(context.Background(), []byte(raw))
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.LimitError {
			t.Fatalf("expected limit, got %v", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := decode(ctx, []byte(`{}`))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func FuzzBridge(f *testing.F) {
	for _, seed := range []string{`{}`, `{"schema":[],"query":"","record":{}}`, `{"schema":[{"name":"x","type":"any"}],"query":"x","record":{"x":1}}`} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, raw string) {
		out := handle(context.Background(), []byte(raw))
		var r response
		if err := json.Unmarshal(out, &r); err != nil {
			t.Fatal(err)
		}
		if r.OK {
			if r.Error != nil || r.Matched == nil {
				t.Fatal("invalid successful response")
			}
		} else if r.Error == nil || r.Matched != nil {
			t.Fatal("invalid failure response")
		}
	})
}
