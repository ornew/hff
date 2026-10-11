package local_test

import (
	"context"
	"encoding/base64"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
)

func TestStrictBase64Conversions(t *testing.T) {
	for _, tc := range []struct {
		text  string
		valid bool
	}{
		{"", true}, {"Zg==", true}, {"Zm8=", true}, {"Zm9v", true}, {"AAEC/w==", true},
		{"Zg", false}, {"Zm8", false}, {"Zh==", false}, {"Zm9=", false},
		{"Zg===", false}, {"Zg==\n", false}, {"Z\rg==", false}, {" Zg==", false},
		{"Zg== ", false}, {"Zg==\t", false}, {"_w==", false}, {"-w==", false}, {"é", false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			f := build(t, `x:frombase64:tobase64=x`)
			got, err := f.Match(context.Background(), record(hff.String(tc.text), hff.Null()))
			if err != nil || got != tc.valid {
				t.Fatalf("got %v %v want %v", got, err, tc.valid)
			}
			invalid := build(t, `x:frombase64=null`)
			got, err = invalid.Match(context.Background(), record(hff.String(tc.text), hff.Null()))
			if err != nil || got == tc.valid {
				t.Fatalf("invalid conversion must be null: %v %v", got, err)
			}
		})
	}
	data := make([]byte, 256)
	for i := range data {
		data[i] = byte(i)
	}
	f := build(t, `x:tobase64=y x:tobase64:frombase64=x`)
	got, err := f.Match(context.Background(), record(hff.Bytes(data), hff.String(base64.StdEncoding.EncodeToString(data))))
	if err != nil || !got {
		t.Fatal("all byte values must round trip:", got, err)
	}
	for _, query := range []string{`x:frombase64=null`, `x:tobase64=null`} {
		got, err := build(t, query).Match(context.Background(), record(hff.Null(), hff.Null()))
		if err != nil || !got {
			t.Fatal("null propagation:", got, err)
		}
	}
}

func TestBase64WorkBudget(t *testing.T) {
	for _, tc := range []struct {
		query string
		value hff.Value
	}{
		{`x:frombase64`, hff.String("YWJjZGVmZ2hpamtsbW5vcA==")},
		{`x:tobase64`, hff.Bytes(make([]byte, 32))},
	} {
		f := build(t, tc.query, local.WithLimits(hff.Limits{MaxSteps: 10}))
		_, err := f.Match(context.Background(), record(tc.value, hff.Null()))
		code(t, err, hff.LimitError)
	}
}
