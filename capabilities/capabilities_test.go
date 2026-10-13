package capabilities_test

import (
	"strings"
	"testing"

	"github.com/ornew/hff/capabilities"
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
	for _, name := range []string{"string.frombase64", "string.frombase64r", "bytes.base64", "bytes.base64r"} {
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

func TestRemovedUnknownIDsAreRejected(t *testing.T) {
	baseline := capabilities.Standard()
	unknown := capabilities.Method("any.tojson")
	for _, policy := range []capabilities.Set{
		baseline.Without(unknown),
		capabilities.None().Allow(unknown).Without(unknown),
		capabilities.None().Without(unknown).Allow(capabilities.Method("any.json")),
	} {
		if err := policy.Check(); err == nil || !strings.Contains(err.Error(), string(unknown)) {
			t.Fatalf("unknown removal not diagnosed: %v", err)
		}
	}
	if err := baseline.Check(); err != nil {
		t.Fatal("derived policy mutated original", err)
	}
	if !baseline.Has(capabilities.Method("any.json")) {
		t.Fatal("original permission changed")
	}
	// Disabled known permissions have no dependency requirements.
	if err := capabilities.None().Without(capabilities.Method("list.any"), capabilities.Regex).Check(); err != nil {
		t.Fatal(err)
	}
}

func TestRemovedExtensionIDsNeedADeclaration(t *testing.T) {
	id := capabilities.Method("service.lookup.v1")
	policy := capabilities.None().Allow(id).Without(id)
	if policy.Has(id) || len(policy.IDs()) != 0 {
		t.Fatal("removal retained permission")
	}
	if err := policy.Check(); err == nil {
		t.Fatal("unknown removed extension accepted")
	}
	if err := policy.CheckWithMethods("service.lookup.v1"); err != nil {
		t.Fatal(err)
	}
	if err := policy.Allow(capabilities.Logical).CheckWithMethods("service.lookup.v1"); err != nil {
		t.Fatal("cloned references corrupted", err)
	}
}
