package local_test

import (
	"context"
	"runtime"
	"testing"
	"unicode"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestLocalProfilesSeparateSupportPolicyAndBudgets(t *testing.T) {
	base := local.Profile()
	if !base.Valid() || base.Capabilities().Specified() {
		t.Fatal("static profile grants service permissions")
	}
	if v, _ := base.Version("go"); v != runtime.Version() {
		t.Fatal("toolchain version missing")
	}
	if v, _ := base.Version("unicode"); v != unicode.Version {
		t.Fatal("Unicode version missing")
	}
	if _, known := base.Version("tz"); known {
		t.Fatal("built-in zones fabricated a TZ database version")
	}
	for _, id := range capabilities.Standard().IDs() {
		if support, known := base.Feature(id); !known || support.Level == hff.Unsupported || support.Level == hff.UnknownSupport {
			t.Fatalf("missing local builtin support: %s", id)
		}
	}
	policy := capabilities.Standard().Without(capabilities.Regex)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("name"))), engine.WithCapabilities(policy),
		engine.WithLimits(hff.Limits{MaxQueryBytes: 500, MaxCallbackDepth: 2}))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(context.Background(), `name="Alice"`)
	if err != nil {
		t.Fatal(err)
	}
	f, err := local.Build(context.Background(), p, local.WithLimits(hff.Limits{MaxSteps: 23, MaxInputDepth: 10}),
		local.WithTimeZones(&local.TimeZoneProfile{Version: "test-tz", Aliases: map[string]string{"Etc/UTC": "UTC"}}))
	if err != nil {
		t.Fatal(err)
	}
	profile := f.Profile()
	if profile.Capabilities().Has(capabilities.Regex) {
		t.Fatal("profile lost the service policy")
	}
	if support, _ := profile.Feature(capabilities.RegexOperators); support.Level != hff.Supported {
		t.Fatal("disabled permission was mistaken for unsupported implementation")
	}
	if compile, ok := profile.CompileLimits(); !ok || compile.MaxQueryBytes != 500 || compile.MaxCallbackDepth != 2 {
		t.Fatal("profile lost engine compile ceilings")
	}
	if input, ok := profile.InputLimits(); !ok || input.MaxInputDepth != 10 {
		t.Fatal("profile lost backend input override")
	}
	if budgets, ok := profile.RuntimeLimits(); !ok || budgets.MaxSteps != 23 || !profile.HasGuarantee(hff.StepBudgetGuarantee) {
		t.Fatal("profile lost runtime ceiling")
	}
	if v, _ := profile.Version("tz"); v != "test-tz" {
		t.Fatal("profile lost the supplied zone version")
	}
	unlimited, err := local.Build(context.Background(), p, local.WithUnlimitedLimits())
	if err != nil {
		t.Fatal(err)
	}
	if budgets, ok := unlimited.Profile().RuntimeLimits(); !ok || budgets.MaxSteps != 0 || unlimited.Profile().HasGuarantee(hff.StepBudgetGuarantee) || unlimited.Profile().HasGuarantee(hff.JSONDepthGuarantee) {
		t.Fatal("unlimited filter claims a finite runtime guarantee")
	}
	var nilFilter *local.Filter
	if nilFilter.Profile().Valid() {
		t.Fatal("nil filter fabricated a profile")
	}
}

func TestLocalProfileReportsRegisteredExtensions(t *testing.T) {
	d := extension(t, schema.StringType, schema.StringType, hff.PropagateNull, 1)
	p := extensionProgram(t, `x:custom="Alice"`, d)
	f, err := local.Build(context.Background(), p, local.WithMethods(local.Implement(d,
		func(_ *local.Call, receiver hff.Value, _ []hff.Value) (hff.Value, error) { return receiver, nil })))
	if err != nil {
		t.Fatal(err)
	}
	profile := f.Profile()
	if methods := profile.Methods(); len(methods) != 1 || !methods[0].Equal(d) {
		t.Fatal("configured extension declaration missing")
	}
	if support, known := profile.Feature(capabilities.Method(d.ID())); !known || support.Level != hff.Conditional || support.Conditions == "" {
		t.Fatal("extension cooperation contract missing")
	}
}
