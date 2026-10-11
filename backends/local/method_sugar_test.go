package local_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
)

func TestEmptyParenthesesSugar(t *testing.T) {
	cases := []struct {
		short, long string
		value       hff.Value
	}{
		{`x:lower:len>2`, `x:lower():len()>2`, hff.String("ABC")},
		{`x:exists`, `x:exists()`, hff.Bool(false)},
		{`x!:exists`, `x!:exists()`, hff.Null()},
		{`x:type="null"`, `x:type()="null"`, hff.Null()},
		{`x:keys[-1]="b"`, `x:keys()[-1]="b"`, hff.Object(map[string]hff.Value{"a": hff.Int(1), "b": hff.Null()})},
		{`x:size!`, `x:size()!`, hff.List()},
		{`x:map(v=>v:lower)=["a","b"]`, `x:map(v=>v:lower())=["a","b"]`, hff.List(hff.String("A"), hff.String("B"))},
		{`x:len (y=null)`, `x:len() (y=null)`, hff.String("a")},
	}
	for _, tc := range cases {
		t.Run(tc.short, func(t *testing.T) {
			a := build(t, tc.short)
			b := build(t, tc.long)
			record := record(tc.value, hff.Null())
			got, err := a.Match(context.Background(), record)
			want, longErr := b.Match(context.Background(), record)
			if err != nil || longErr != nil || got != want {
				t.Fatalf("sugar changed result: %v %v vs %v %v", got, err, want, longErr)
			}
		})
	}
	e := configured(t)
	for _, q := range []string{`x:contains`, `x:map`, `x:in`, `x:startswith`, `x:unknown`} {
		_, err := e.Compile(context.Background(), q)
		code(t, err, hff.TypeError)
	}
	// A method with optional arguments accepts omitted parentheses too.
	for _, q := range []string{`x:year=2026`, `x:year()=2026`} {
		if _, err := e.Compile(context.Background(), q); err != nil {
			t.Fatal(q, err)
		}
	}
}
