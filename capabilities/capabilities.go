package capabilities

import (
	"fmt"
	"sort"
)

// ID is a stable permission identifier used by an explicit capability Set.
// IDs describe compile-time HFF features; they do not authorize service
// operations or provide backend implementations.
type ID string

const (
	// Logical permits Boolean query composition and truth evaluation.
	Logical ID = "operator:logical"
	// Comparison permits value and null comparisons.
	Comparison ID = "operator:comparison"
	// Arithmetic permits arithmetic operators.
	Arithmetic ID = "operator:arithmetic"
	// Access permits field access.
	Access ID = "access:field"
	// Index permits list and map indexing.
	Index ID = "access:index"
	// Guard permits postfix no-value guards.
	Guard ID = "access:guard"
	// TypeGuard permits runtime type narrowing.
	TypeGuard ID = "access:type-guard"
	// Collections permits list and object literals.
	Collections ID = "literal:collection"
	// Callbacks permits callback expressions.
	Callbacks ID = "expression:callback"
	// NestedCallbacks permits nested callbacks and requires Callbacks.
	NestedCallbacks ID = "expression:nested-callback"
	// ListEquality permits structural list comparisons.
	ListEquality ID = "comparison:list-equality"
	// ObjectEquality permits structural object comparisons.
	ObjectEquality ID = "comparison:object-equality"
	// StructuralEquality is a group alias for both structural equality IDs.
	StructuralEquality ID = "group:structural-equality"
	// Regex is a group alias for RegexOperators and string.matches.
	Regex ID = "group:regex"
	// RegexOperators permits regex match operators.
	RegexOperators ID = "operator:regex"
)

// Method identifies a built-in method or declared extension, for example
// Method("string.matches") or Method("service.lookup.v1"). For an extension,
// pass the declaration's ID suffix (without the "method:" prefix), then both
// declare it in the Engine and explicitly Allow this ID. This grants permission
// only; a backend implementation must be registered separately.
func Method(name string) ID { return ID("method:" + name) }

var builtins = map[ID][]ID{}

func init() {
	for _, id := range []ID{Logical, Comparison, Arithmetic, Access, Index, Guard, TypeGuard, Collections, Callbacks, RegexOperators} {
		builtins[id] = nil
	}
	builtins[NestedCallbacks] = []ID{Callbacks}
	builtins[ListEquality] = nil
	builtins[ObjectEquality] = nil
	for _, name := range []string{"any.type", "any.json", "any.string", "any.in", "any.exists", "bool.and", "bool.or", "bool.not", "number.round", "number.ceil", "number.floor", "string.len", "string.contains", "string.matches", "string.startswith", "string.endswith", "string.lower", "string.upper", "string.timestamp", "string.duration", "string.bool", "string.number", "string.fromjson", "string.concat", "bytes.len", "list.size", "list.count", "list.filter", "list.sum", "list.min", "list.max", "list.has", "list.contains", "list.unique", "list.sort", "list.concat", "list.any", "list.all", "list.map", "list.flatmap", "list.foldl", "list.foldr", "object.size", "object.has", "object.get", "object.keys", "object.values", "object.items", "timestamp.year", "timestamp.month", "timestamp.dayofmonth", "timestamp.dayofyear", "timestamp.hours", "timestamp.minutes", "timestamp.seconds", "timestamp.milliseconds", "timestamp.string", "timestamp.after", "timestamp.before", "timestamp.add", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds"} {
		builtins[Method(name)] = nil
	}
	for _, name := range []string{"list.any", "list.all", "list.filter", "list.map", "list.flatmap", "list.foldl", "list.foldr"} {
		builtins[Method(name)] = []ID{Callbacks}
	}
	for _, name := range []string{"string.frombase64", "string.frombase64r", "bytes.base64", "bytes.base64r"} {
		builtins[Method(name)] = nil
	}
}

// Set is an immutable capability policy. Its zero value means unspecified;
// select None or Standard (or derive a policy with Allow and Without) before
// passing it to engine.New. For example, start with Standard().Without(Regex)
// to remove both regex operators and string.matches. Set is a compile-time
// permission policy, not a backend implementation or service authorization
// policy.
type Set struct {
	specified  bool
	ids        map[ID]bool
	referenced map[ID]bool
}

// None returns an explicitly empty policy. It differs from the zero Set, which
// is unspecified and rejected by engine.New. Add only the permissions a
// restricted engine should compile:
//
//	policy := capabilities.None().Allow(capabilities.Logical, capabilities.Access)
//	engine.New(engine.WithSchema(schemaType), engine.WithCapabilities(policy))
func None() Set { return Set{specified: true, ids: map[ID]bool{}} }

// Standard returns the built-in policy, including structural equality and
// built-in methods. It does not enable extension IDs; each extension needs an
// explicit Allow(Method(id)) and a matching Engine declaration. It is a
// permission preset, not a claim that every backend implements every feature.
func Standard() Set {
	s := None()
	for id := range builtins {
		s.ids[id] = true
	}
	return s
}

// Specified reports whether a policy was explicitly selected rather than left
// at the zero value. None is specified even though it contains no IDs.
func (s Set) Specified() bool { return s.specified }
func expand(id ID) []ID {
	if id == StructuralEquality {
		return []ID{ListEquality, ObjectEquality}
	}
	if id == Regex {
		return []ID{RegexOperators, Method("string.matches")}
	}
	return []ID{id}
}
func (s Set) clone() Set {
	out := None()
	for id := range s.ids {
		out.ids[id] = true
	}
	if len(s.referenced) > 0 {
		out.referenced = make(map[ID]bool, len(s.referenced))
		for id := range s.referenced {
			out.referenced[id] = true
		}
	}
	return out
}

// Allow returns a new Set with the requested IDs enabled; s is unchanged.
// StructuralEquality expands to ListEquality and ObjectEquality, and Regex
// expands to RegexOperators and string.matches. Dependencies are not added
// implicitly: for example, NestedCallbacks requires Callbacks, so enable both
// before Check:
//
//	policy := capabilities.None().Allow(capabilities.Callbacks, capabilities.NestedCallbacks)
func (s Set) Allow(ids ...ID) Set {
	out := s.clone()
	for _, id := range ids {
		for _, item := range expand(id) {
			if out.referenced == nil {
				out.referenced = make(map[ID]bool)
			}
			out.referenced[item] = true
			out.ids[item] = true
		}
	}
	return out
}

// Without returns a new Set with the requested IDs removed; s is unchanged.
// Removing a group alias removes every member. Removing a dependency while
// leaving a dependent permission can make Check fail. IDs supplied to Without
// must be known even when they are not enabled; Check reports unknown IDs.
func (s Set) Without(ids ...ID) Set {
	out := s.clone()
	for _, id := range ids {
		for _, item := range expand(id) {
			if out.referenced == nil {
				out.referenced = make(map[ID]bool)
			}
			out.referenced[item] = true
			delete(out.ids, item)
		}
	}
	return out
}

// Has reports whether id is enabled. For a group alias, it returns true only
// when every expanded member ID is enabled. The zero Set therefore reports
// false for every permission.
func (s Set) Has(id ID) bool {
	for _, item := range expand(id) {
		if !s.ids[item] {
			return false
		}
	}
	return true
}

// IDs returns a defensive copy of enabled member IDs in lexical order. Group
// aliases are expanded, so the result lists their constituent IDs.
func (s Set) IDs() []ID {
	out := make([]ID, 0, len(s.ids))
	for id := range s.ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Check validates that the Set is specified, all selected and removed IDs are
// known built-in permissions, and enabled permissions have their dependencies.
// It never enables missing permissions. Use CheckWithMethods when the policy selects or removes declared
// extension IDs.
func (s Set) Check() error { return s.CheckWithMethods() }

// CheckWithMethods validates the Set while treating methodIDs as known
// extension declaration IDs. It does not add permissions: an enabled
// capabilities.Method(id) is still required. Removed extension IDs also require
// their declaration ID in methodIDs. Unknown IDs, an unspecified Set,
// or missing dependencies return an error. engine.New wraps policy errors in
// an HFF ConfigurationError when validating Engine options.
func (s Set) CheckWithMethods(methodIDs ...string) error {
	extensions := make(map[ID]bool, len(methodIDs))
	for _, id := range methodIDs {
		extensions[Method(id)] = true
	}
	if !s.specified {
		return fmt.Errorf("capabilities must be explicitly selected")
	}
	// Validate all explicitly supplied IDs, even if a later selection removed
	// them. Otherwise a typo in Without silently leaves a permission enabled.
	var referenced []ID
	for id := range s.referenced {
		referenced = append(referenced, id)
	}
	if len(referenced) > 1 {
		sort.Slice(referenced, func(i, j int) bool { return referenced[i] < referenced[j] })
	}
	for _, id := range referenced {
		if _, known := builtins[id]; !known && !extensions[id] {
			return fmt.Errorf("unknown capability %q", id)
		}
	}
	for _, id := range s.IDs() {
		dependencies, known := builtins[id]
		if !known && !extensions[id] {
			return fmt.Errorf("unknown capability %q", id)
		}
		for _, dependency := range dependencies {
			if !s.Has(dependency) {
				return fmt.Errorf("capability %q requires %q", id, dependency)
			}
		}
	}
	return nil
}
