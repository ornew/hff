package capabilities_test

import (
	"github.com/ornew/hff/capabilities"
	"testing"
)

func TestSelectionDoesNotMutateProfiles(t *testing.T) {
	standard := capabilities.Standard()
	restricted := standard.Without(capabilities.Regex)
	if !standard.Has(capabilities.Regex) || restricted.Has(capabilities.Regex) || restricted.Has(capabilities.Method("string.matches")) {
		t.Fatal("regex selection or immutability broken")
	}
	if err := restricted.Check(); err != nil {
		t.Fatal(err)
	}
	if err := capabilities.None().Allow(capabilities.Method("list.any")).Check(); err == nil {
		t.Fatal("missing callback dependency accepted")
	}
	if err := capabilities.None().Allow(capabilities.Method("list.any"), capabilities.Callbacks).Check(); err != nil {
		t.Fatal(err)
	}
	ids := standard.IDs()
	ids[0] = "unknown"
	if err := standard.Check(); err != nil {
		t.Fatal("returned IDs mutate set")
	}
	if (capabilities.Set{}).Specified() || !capabilities.None().Specified() {
		t.Fatal("empty and unspecified policies conflated")
	}
}

func TestBase64MethodsCanBeSelected(t *testing.T) {
	for _, name := range []string{"string.frombase64", "bytes.tobase64"} {
		id := capabilities.Method(name)
		if !capabilities.Standard().Has(id) {
			t.Fatalf("standard profile omits %s", name)
		}
		selected := capabilities.None().Allow(id)
		if err := selected.Check(); err != nil {
			t.Fatalf("explicitly selecting %s: %v", name, err)
		}
		if selected.Without(id).Has(id) {
			t.Fatalf("cannot disable %s", name)
		}
	}
}
