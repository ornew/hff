package hff

import (
	"sort"
	"unicode/utf8"

	"github.com/ornew/hff/capabilities"
)

// SupportLevel describes backend support separately from service permission.
// Missing feature entries are unknown and must not be treated as supported.
type SupportLevel uint8

const (
	// UnknownSupport means the profile makes no claim about the feature.
	UnknownSupport SupportLevel = iota
	// Supported means the backend declares support, subject to Build validation.
	Supported
	// Conditional means support requires the stated conditions.
	Conditional
	// Unsupported means this backend profile declares the feature unavailable.
	Unsupported
)

// FeatureSupport records the backend's status for one capability ID.
type FeatureSupport struct {
	// ID is the capability or feature identifier being described.
	ID capabilities.ID
	// Level is the support status; an omitted entry remains unknown.
	Level SupportLevel
	// Conditions explains requirements when Level is Conditional.
	Conditions string
}

// CompatibilityVersion names an implementation or supplied data version.
// A missing component is unknown; profiles never infer a database's versions.
type CompatibilityVersion struct{ Component, Version string }

// GuaranteeID names one affirmative backend guarantee.
type GuaranteeID string

const (
	// InputValidationGuarantee means the backend validates host input records.
	InputValidationGuarantee GuaranteeID = "input-validation"
	// EvaluationOrderGuarantee means evaluation follows HFF's left-to-right order.
	EvaluationOrderGuarantee GuaranteeID = "left-to-right-evaluation"
	// CancellationGuarantee means the backend honors cancellation at documented checkpoints.
	CancellationGuarantee GuaranteeID = "context-cancellation"
	// StepBudgetGuarantee means the backend enforces its runtime step ceiling.
	StepBudgetGuarantee GuaranteeID = "step-budget"
	// GeneratedValuesGuarantee means the backend enforces its generated-value ceiling.
	GeneratedValuesGuarantee GuaranteeID = "generated-values-budget"
	// JSONDepthGuarantee means the backend enforces its JSON depth ceiling.
	JSONDepthGuarantee GuaranteeID = "json-depth-budget"
)

// ProfileDetail holds backend-specific descriptive information. It grants no
// capabilities and is not a configuration option or execution guarantee.
type ProfileDetail struct{ Name, Value string }

// ProfileConfig declares backend facts known to the caller or implementation.
// NewProfile snapshots its slices and limit pointers. Nil stage limits mean
// unknown; non-nil zero fields mean explicitly unbounded for that stage.
type ProfileConfig struct {
	// Backend is a stable backend name, such as "local" or "postgresql".
	Backend string
	// Features lists known feature statuses; omitted IDs remain unknown.
	Features []FeatureSupport
	// Versions lists only known implementation or explicitly supplied data versions.
	Versions []CompatibilityVersion
	// Guarantees lists affirmative guarantees, not missing behavior.
	Guarantees []GuaranteeID
	// Details contains backend-specific descriptive values.
	Details []ProfileDetail
	// Methods contains extension declarations; it does not enable permissions.
	Methods []MethodDeclaration
	// Capabilities is the effective service policy; its zero value is unknown.
	Capabilities capabilities.Set
	// Nil limits mean unknown or outside this backend's responsibility. Non-nil
	// limits describe resolved ceilings; zero applicable fields mean explicitly
	// unbounded. Fields belonging to a different stage are not applicable.
	CompileLimits *Limits
	InputLimits   *Limits
	RuntimeLimits *Limits
}

// Profile is an immutable declaration of known backend facts. Build remains
// authoritative for a specific program, mapping, and configuration.
type Profile struct{ config ProfileConfig }

// NewProfile validates and snapshots a profile. Empty or duplicate entries and
// invalid stage limits return a ConfigurationError.
func NewProfile(config ProfileConfig) (Profile, error) {
	fail := func(message string) (Profile, error) {
		return Profile{}, &Error{Code: ConfigurationError, Message: message}
	}
	valid := func(s string) bool { return s != "" && utf8.ValidString(s) }
	if !valid(config.Backend) {
		return fail("profile requires a valid backend name")
	}
	config.Features = append([]FeatureSupport(nil), config.Features...)
	config.Versions = append([]CompatibilityVersion(nil), config.Versions...)
	config.Guarantees = append([]GuaranteeID(nil), config.Guarantees...)
	config.Details = append([]ProfileDetail(nil), config.Details...)
	config.Methods = append([]MethodDeclaration(nil), config.Methods...)
	for _, limits := range []**Limits{&config.CompileLimits, &config.InputLimits, &config.RuntimeLimits} {
		if *limits == nil {
			continue
		}
		copy := **limits
		if _, err := copy.Resolve(); err != nil {
			return fail("invalid profile limits: " + err.Error())
		}
		*limits = &copy
	}
	sort.Slice(config.Features, func(i, j int) bool { return config.Features[i].ID < config.Features[j].ID })
	sort.Slice(config.Versions, func(i, j int) bool { return config.Versions[i].Component < config.Versions[j].Component })
	sort.Slice(config.Guarantees, func(i, j int) bool { return config.Guarantees[i] < config.Guarantees[j] })
	sort.Slice(config.Details, func(i, j int) bool { return config.Details[i].Name < config.Details[j].Name })
	sort.Slice(config.Methods, func(i, j int) bool { return config.Methods[i].ID() < config.Methods[j].ID() })
	for i, item := range config.Features {
		if !valid(string(item.ID)) || item.Level < Supported || item.Level > Unsupported || !utf8.ValidString(item.Conditions) || (item.Level == Conditional && item.Conditions == "") || (i > 0 && config.Features[i-1].ID == item.ID) {
			return fail("invalid or duplicate profile feature")
		}
	}
	for i, item := range config.Versions {
		if !valid(item.Component) || !valid(item.Version) || (i > 0 && config.Versions[i-1].Component == item.Component) {
			return fail("invalid or duplicate profile version")
		}
	}
	for i, item := range config.Guarantees {
		if !valid(string(item)) || (i > 0 && config.Guarantees[i-1] == item) {
			return fail("invalid or duplicate profile guarantee")
		}
	}
	for i, item := range config.Details {
		if !valid(item.Name) || !utf8.ValidString(item.Value) || (i > 0 && config.Details[i-1].Name == item.Name) {
			return fail("invalid or duplicate profile detail")
		}
	}
	for i, method := range config.Methods {
		if !method.Valid() || (i > 0 && config.Methods[i-1].ID() == method.ID()) {
			return fail("invalid or duplicate profile method declaration")
		}
	}
	return Profile{config: config}, nil
}

// Valid reports whether p contains a valid profile declaration.
func (p Profile) Valid() bool { return p.config.Backend != "" }

// Backend returns the backend name, or an empty string for an invalid profile.
func (p Profile) Backend() string { return p.config.Backend }

// Features returns a defensive copy of known feature statuses.
func (p Profile) Features() []FeatureSupport {
	return append([]FeatureSupport(nil), p.config.Features...)
}

// Versions returns a defensive copy of known version components.
func (p Profile) Versions() []CompatibilityVersion {
	return append([]CompatibilityVersion(nil), p.config.Versions...)
}

// Guarantees returns a defensive copy of affirmative guarantees.
func (p Profile) Guarantees() []GuaranteeID {
	return append([]GuaranteeID(nil), p.config.Guarantees...)
}

// Details returns a defensive copy of backend-specific descriptive values.
func (p Profile) Details() []ProfileDetail { return append([]ProfileDetail(nil), p.config.Details...) }

// Methods returns the immutable declarations of configured extensions.
// Their presence does not enable those methods in the service's capability set.
func (p Profile) Methods() []MethodDeclaration {
	return append([]MethodDeclaration(nil), p.config.Methods...)
}

// Capabilities returns the declared service policy. Its zero value is
// unspecified and does not mean all capabilities are allowed.
func (p Profile) Capabilities() capabilities.Set { return p.config.Capabilities }

// CompileLimits reports query byte, syntax depth, callback depth, and compile
// work ceilings. Other fields are not applicable.
func (p Profile) CompileLimits() (Limits, bool) { return profileLimits(p.config.CompileLimits) }

// InputLimits reports input depth and value count ceilings.
func (p Profile) InputLimits() (Limits, bool) { return profileLimits(p.config.InputLimits) }

// RuntimeLimits reports evaluation steps, generated values, and JSON depth.
func (p Profile) RuntimeLimits() (Limits, bool) { return profileLimits(p.config.RuntimeLimits) }
func profileLimits(limits *Limits) (Limits, bool) {
	if limits == nil {
		return Limits{}, false
	}
	return *limits, true
}

// Feature looks up a declared feature. The bool is false when the ID has no
// entry. Unknown support is represented by an absent entry.
func (p Profile) Feature(id capabilities.ID) (FeatureSupport, bool) {
	i := sort.Search(len(p.config.Features), func(i int) bool { return p.config.Features[i].ID >= id })
	if i < len(p.config.Features) && p.config.Features[i].ID == id {
		return p.config.Features[i], true
	}
	return FeatureSupport{}, false
}

// Version returns a known component version without inferring one from a backend.
func (p Profile) Version(component string) (string, bool) {
	i := sort.Search(len(p.config.Versions), func(i int) bool { return p.config.Versions[i].Component >= component })
	if i < len(p.config.Versions) && p.config.Versions[i].Component == component {
		return p.config.Versions[i].Version, true
	}
	return "", false
}

// HasGuarantee reports whether this profile declares the affirmative guarantee.
func (p Profile) HasGuarantee(id GuaranteeID) bool {
	i := sort.Search(len(p.config.Guarantees), func(i int) bool { return p.config.Guarantees[i] >= id })
	return i < len(p.config.Guarantees) && p.config.Guarantees[i] == id
}
