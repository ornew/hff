package hff_test

import (
	"errors"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
)

func TestProfileOwnsConfigurationAndAccessors(t *testing.T) {
	limits := hff.Limits{MaxSteps: 10}
	config := hff.ProfileConfig{Backend: "test",
		Features:     []hff.FeatureSupport{{ID: "z", Level: hff.Unsupported}, {ID: "a", Level: hff.Conditional, Conditions: "declared domain"}},
		Versions:     []hff.CompatibilityVersion{{Component: "unicode", Version: "versioned"}},
		Guarantees:   []hff.GuaranteeID{hff.StepBudgetGuarantee},
		Details:      []hff.ProfileDetail{{Name: "binding", Value: "explicit"}},
		Capabilities: capabilities.Standard(), RuntimeLimits: &limits}
	profile, err := hff.NewProfile(config)
	if err != nil {
		t.Fatal(err)
	}
	config.Features[0].Level = hff.Supported
	config.Versions[0].Version = "changed"
	config.Guarantees[0] = hff.CancellationGuarantee
	config.Details[0].Value = "changed"
	limits.MaxSteps = 20
	features := profile.Features()
	if len(features) != 2 || features[0].ID != "a" || features[1].Level != hff.Unsupported {
		t.Fatalf("feature snapshot/order: %+v", features)
	}
	features[0].Conditions = "changed"
	versions := profile.Versions()
	versions[0].Version = "changed"
	guarantees := profile.Guarantees()
	guarantees[0] = hff.CancellationGuarantee
	details := profile.Details()
	details[0].Value = "changed"
	if feature, ok := profile.Feature("a"); !ok || feature.Conditions != "declared domain" {
		t.Fatal("feature accessor exposed mutable state")
	}
	if version, ok := profile.Version("unicode"); !ok || version != "versioned" {
		t.Fatal("version accessor exposed mutable state")
	}
	if !profile.HasGuarantee(hff.StepBudgetGuarantee) || profile.HasGuarantee(hff.CancellationGuarantee) || profile.Details()[0].Value != "explicit" {
		t.Fatal("guarantee/detail accessor exposed mutable state")
	}
	resolved, known := profile.RuntimeLimits()
	if !known || resolved.MaxSteps != 10 {
		t.Fatal("limit configuration was not copied")
	}
	resolved.MaxSteps = 100
	if again, _ := profile.RuntimeLimits(); again.MaxSteps != 10 {
		t.Fatal("limits accessor exposed mutable state")
	}
	if _, known := profile.InputLimits(); known {
		t.Fatal("missing limits must remain unknown")
	}
	if _, known := profile.Version("tz"); known {
		t.Fatal("missing version must remain unknown")
	}
	if feature, known := profile.Feature("missing"); known || feature.Level != hff.UnknownSupport {
		t.Fatal("missing feature must remain unknown")
	}
	if !profile.Valid() || profile.Backend() != "test" || !profile.Capabilities().Has(capabilities.Logical) {
		t.Fatal("profile identity/policy lost")
	}
}

func TestProfileInvalidConfigurations(t *testing.T) {
	for _, config := range []hff.ProfileConfig{
		{}, {Backend: "\xff"},
		{Backend: "test", Features: []hff.FeatureSupport{{ID: "x", Level: hff.Conditional}}},
		{Backend: "test", Features: []hff.FeatureSupport{{ID: "x", Level: hff.Supported}, {ID: "x", Level: hff.Unsupported}}},
		{Backend: "test", Versions: []hff.CompatibilityVersion{{Component: "go", Version: ""}}},
		{Backend: "test", Versions: []hff.CompatibilityVersion{{Component: "go", Version: "x"}, {Component: "go", Version: "y"}}},
		{Backend: "test", Guarantees: []hff.GuaranteeID{hff.StepBudgetGuarantee, hff.StepBudgetGuarantee}},
		{Backend: "test", Details: []hff.ProfileDetail{{Name: "x"}, {Name: "x"}}},
		{Backend: "test", Methods: []hff.MethodDeclaration{{}}},
		{Backend: "test", RuntimeLimits: &hff.Limits{MaxSteps: -1}},
	} {
		profile, err := hff.NewProfile(config)
		var typed *hff.Error
		if err == nil || profile.Valid() || !errors.As(err, &typed) || typed.Code != hff.ConfigurationError {
			t.Fatalf("invalid profile accepted: %+v, %v", config, err)
		}
	}
	var empty hff.Profile
	if empty.Valid() || empty.Capabilities().Specified() || empty.HasGuarantee(hff.StepBudgetGuarantee) {
		t.Fatal("zero profile fabricated known facts")
	}
}

func TestProfileExtensionDeclarationsAreImmutable(t *testing.T) {
	declaration, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "service.identity.v1", Name: "identity",
		Receiver: hff.PrimitiveType(hff.StringType), Result: hff.PrimitiveType(hff.StringType), BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	methods := []hff.MethodDeclaration{declaration}
	profile, err := hff.NewProfile(hff.ProfileConfig{Backend: "test", Methods: methods})
	if err != nil {
		t.Fatal(err)
	}
	methods[0] = hff.MethodDeclaration{}
	copy := profile.Methods()
	copy[0] = hff.MethodDeclaration{}
	if got := profile.Methods(); len(got) != 1 || !got[0].Equal(declaration) {
		t.Fatal("profile exposed its method declaration slice")
	}
	if _, err := hff.NewProfile(hff.ProfileConfig{Backend: "test", Methods: []hff.MethodDeclaration{declaration, declaration}}); err == nil {
		t.Fatal("duplicate declaration accepted")
	}
}
