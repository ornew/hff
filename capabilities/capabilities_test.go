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

func TestNestedCallbackDependency(t *testing.T) {
	if !capabilities.Standard().Has(capabilities.NestedCallbacks) {
		t.Fatal("standard profile must support nested callbacks")
	}
	if err := capabilities.None().Allow(capabilities.NestedCallbacks).Check(); err == nil {
		t.Fatal("nested callbacks require callbacks")
	}
	if err := capabilities.None().Allow(capabilities.Callbacks, capabilities.NestedCallbacks).Check(); err != nil {
		t.Fatal(err)
	}
}

func TestStructuralEqualityGroup(t *testing.T) {
	standard := capabilities.Standard()
	without := standard.Without(capabilities.StructuralEquality)
	if !standard.Has(capabilities.StructuralEquality) || without.Has(capabilities.ListEquality) || without.Has(capabilities.ObjectEquality) {
		t.Fatal("group removal mutated the standard profile or left a permission")
	}
	listOnly := without.Allow(capabilities.ListEquality)
	if !listOnly.Has(capabilities.ListEquality) || listOnly.Has(capabilities.StructuralEquality) || listOnly.Has(capabilities.ObjectEquality) {
		t.Fatal("list and object permissions are not independent")
	}
	if err := capabilities.None().Allow(capabilities.StructuralEquality).Check(); err != nil {
		t.Fatal(err)
	}
	if !without.Allow(capabilities.StructuralEquality).Has(capabilities.StructuralEquality) {
		t.Fatal("group cannot be restored")
	}
}
