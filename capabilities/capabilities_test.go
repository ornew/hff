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
