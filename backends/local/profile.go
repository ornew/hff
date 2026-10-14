package local

import (
	"runtime"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
)

// Profile describes built-in support and the implementation's compatibility
// versions. It does not grant service permissions or load time-zone data.
func Profile() hff.Profile {
	return localProfile(nil)
}

// Profile reports the built filter's actual budgets, time-zone snapshot, and
// registered extension declarations. A nil filter returns an invalid profile.
func (f *Filter) Profile() hff.Profile {
	if f == nil || !f.program.Valid() {
		return hff.Profile{}
	}
	return localProfile(f)
}

func localProfile(f *Filter) hff.Profile {
	config := hff.ProfileConfig{
		Backend: "local",
		Versions: []hff.CompatibilityVersion{
			{Component: "go", Version: runtime.Version()},
			{Component: "unicode", Version: unicode.Version},
			// Go's regexp parser and engine are versioned with the toolchain;
			// this is not a claim to implement a specific upstream RE2 release.
			{Component: "regex", Version: "Go regexp " + runtime.Version()},
		},
		Guarantees: []hff.GuaranteeID{hff.InputValidationGuarantee, hff.EvaluationOrderGuarantee, hff.CancellationGuarantee},
		Details: []hff.ProfileDetail{
			{Name: "float.model", Value: "Go float64 approximate binary64; Float results and text need not match other backends at rounding boundaries"},
			{Name: "float.parsing", Value: "Go strconv float64 input; subnormals and underflow to signed zero are accepted, overflow becomes null"},
			{Name: "float.arithmetic", Value: "Go float64 arithmetic; non-finite results and zero division become null"},
			{Name: "float.rounding", Value: "Go math Round (midpoints away from zero), Ceil and Floor"},
			{Name: "float.aggregation", Value: "sum and avg keep Int contributions exact until the first Float, then add in list order with Go float64; avg divides the final total by the contributing count; non-finite intermediate or final results become null"},
			{Name: "float.format", Value: "Go strconv shortest round-trip Float text with a decimal/exponent subtype marker and signed zero"},
			{Name: "regex.syntax", Value: "RE2-compatible syntax excluding \\C; substring matching"},
			{Name: "timezone.builtin", Value: "UTC and fixed offsets; named zones require explicitly supplied TZif data"},
			{Name: "extensions.contract", Value: "extensions must be pure and concurrent-safe and cooperate with cancellation and cost accounting"},
		},
	}
	for _, id := range capabilities.Standard().IDs() {
		feature := hff.FeatureSupport{ID: id, Level: hff.Supported}
		if strings.HasPrefix(string(id), "method:") && !supportedMethod(strings.TrimPrefix(string(id), "method:")) {
			feature.Level = hff.Unsupported
		}
		if strings.HasPrefix(string(id), "method:timestamp.") && id != capabilities.Method("timestamp.after") && id != capabilities.Method("timestamp.before") && id != capabilities.Method("timestamp.add") {
			feature.Level = hff.Conditional
			feature.Conditions = "UTC and fixed offsets are built in; named zones must be present in the supplied time-zone profile"
		}
		config.Features = append(config.Features, feature)
	}
	limits := hff.DefaultLimits()
	if f != nil {
		limits = f.config.Limits
		config.Capabilities = f.program.Capabilities()
		configured := f.program.Limits()
		compile := hff.Limits{MaxQueryBytes: configured.MaxQueryBytes, MaxSyntaxDepth: configured.MaxSyntaxDepth,
			MaxCallbackDepth: configured.MaxCallbackDepth, MaxCompileSteps: configured.MaxCompileSteps}
		config.CompileLimits = &compile
		if f.timeZones.version != "" {
			config.Versions = append(config.Versions, hff.CompatibilityVersion{Component: "tz", Version: f.timeZones.version})
		}
		var names []string
		for name := range f.timeZones.names {
			names = append(names, name)
		}
		sort.Strings(names)
		config.Details = append(config.Details, hff.ProfileDetail{Name: "timezone.names", Value: strings.Join(names, ", ")})
		for id, method := range f.methods {
			config.Methods = append(config.Methods, method.declaration)
			config.Features = append(config.Features, hff.FeatureSupport{ID: capabilities.Method(id), Level: hff.Conditional,
				Conditions: "registered implementation must obey its declared types, purity, cancellation, and cost contract"})
		}
	}
	input := hff.Limits{MaxInputDepth: limits.MaxInputDepth, MaxInputValues: limits.MaxInputValues}
	runtime := hff.Limits{MaxSteps: limits.MaxSteps, MaxGeneratedValues: limits.MaxGeneratedValues, MaxJSONDepth: limits.MaxJSONDepth}
	config.InputLimits, config.RuntimeLimits = &input, &runtime
	for _, budget := range []struct {
		name      string
		value     int
		guarantee hff.GuaranteeID
	}{
		{"MaxSteps", limits.MaxSteps, hff.StepBudgetGuarantee},
		{"MaxGeneratedValues", limits.MaxGeneratedValues, hff.GeneratedValuesGuarantee},
		{"MaxJSONDepth", limits.MaxJSONDepth, hff.JSONDepthGuarantee},
		{"MaxInputDepth", limits.MaxInputDepth, ""},
		{"MaxInputValues", limits.MaxInputValues, ""},
	} {
		value := strconv.Itoa(budget.value)
		if budget.value == 0 {
			value = "unlimited"
		}
		config.Details = append(config.Details, hff.ProfileDetail{Name: "limits." + budget.name, Value: value})
		if budget.value > 0 && budget.guarantee != "" {
			config.Guarantees = append(config.Guarantees, budget.guarantee)
		}
	}
	profile, err := hff.NewProfile(config)
	if err != nil {
		panic(err) // All entries above are validated internal declarations.
	}
	return profile
}
