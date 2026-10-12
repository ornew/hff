package hff

import (
	"sort"
	"unicode/utf8"

	"github.com/ornew/hff/capabilities"
)

// SupportLevel describes backend support, not the service's permission policy.
// Missing entries are unknown and must not be interpreted as supported.
type SupportLevel uint8

const (
	UnknownSupport SupportLevel = iota
	Supported
	Conditional
	Unsupported
)

type FeatureSupport struct {
	ID         capabilities.ID
	Level      SupportLevel
	Conditions string
}

// CompatibilityVersion names an implementation or supplied data version.
// A missing component is unknown; profiles never infer a database's versions.
type CompatibilityVersion struct{ Component, Version string }

type GuaranteeID string

const (
	InputValidationGuarantee GuaranteeID = "input-validation"
	EvaluationOrderGuarantee GuaranteeID = "left-to-right-evaluation"
	CancellationGuarantee    GuaranteeID = "context-cancellation"
	StepBudgetGuarantee      GuaranteeID = "step-budget"
	GeneratedValuesGuarantee GuaranteeID = "generated-values-budget"
	JSONDepthGuarantee       GuaranteeID = "json-depth-budget"
)

// ProfileDetail holds backend-specific descriptive information. It grants no
// capabilities and is not a configuration option or an execution guarantee.
type ProfileDetail struct{ Name, Value string }

type ProfileConfig struct {
	Backend    string
	Features   []FeatureSupport
	Versions   []CompatibilityVersion
	Guarantees []GuaranteeID
	Details    []ProfileDetail
	Methods    []MethodDeclaration
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

func (p Profile) Valid() bool     { return p.config.Backend != "" }
func (p Profile) Backend() string { return p.config.Backend }
func (p Profile) Features() []FeatureSupport {
	return append([]FeatureSupport(nil), p.config.Features...)
}
func (p Profile) Versions() []CompatibilityVersion {
	return append([]CompatibilityVersion(nil), p.config.Versions...)
}
func (p Profile) Guarantees() []GuaranteeID {
	return append([]GuaranteeID(nil), p.config.Guarantees...)
}
func (p Profile) Details() []ProfileDetail { return append([]ProfileDetail(nil), p.config.Details...) }

// Methods returns the immutable declarations of configured extensions.
// Their presence does not enable those methods in the service's capability set.
func (p Profile) Methods() []MethodDeclaration {
	return append([]MethodDeclaration(nil), p.config.Methods...)
}
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
func (p Profile) Feature(id capabilities.ID) (FeatureSupport, bool) {
	i := sort.Search(len(p.config.Features), func(i int) bool { return p.config.Features[i].ID >= id })
	if i < len(p.config.Features) && p.config.Features[i].ID == id {
		return p.config.Features[i], true
	}
	return FeatureSupport{}, false
}
func (p Profile) Version(component string) (string, bool) {
	i := sort.Search(len(p.config.Versions), func(i int) bool { return p.config.Versions[i].Component >= component })
	if i < len(p.config.Versions) && p.config.Versions[i].Component == component {
		return p.config.Versions[i].Version, true
	}
	return "", false
}
func (p Profile) HasGuarantee(id GuaranteeID) bool {
	i := sort.Search(len(p.config.Guarantees), func(i int) bool { return p.config.Guarantees[i] >= id })
	return i < len(p.config.Guarantees) && p.config.Guarantees[i] == id
}
