// Package capabilities defines immutable semantic permissions, independent of grammar.
package capabilities

import (
	"fmt"
	"sort"
)

type ID string

const (
	Logical            ID = "operator:logical"
	Comparison         ID = "operator:comparison"
	Arithmetic         ID = "operator:arithmetic"
	Access             ID = "access:field"
	Index              ID = "access:index"
	Guard              ID = "access:guard"
	TypeGuard          ID = "access:type-guard"
	Collections        ID = "literal:collection"
	Callbacks          ID = "expression:callback"
	NestedCallbacks    ID = "expression:nested-callback"
	ListEquality       ID = "comparison:list-equality"
	ObjectEquality     ID = "comparison:object-equality"
	StructuralEquality ID = "group:structural-equality"
	Regex              ID = "group:regex"
	RegexOperators     ID = "operator:regex"
)

// Method identifies a built-in or declared extension, e.g. Method("string.matches").
func Method(name string) ID { return ID("method:" + name) }

var builtins = map[ID][]ID{}

func init() {
	for _, id := range []ID{Logical, Comparison, Arithmetic, Access, Index, Guard, TypeGuard, Collections, Callbacks, RegexOperators} {
		builtins[id] = nil
	}
	builtins[NestedCallbacks] = []ID{Callbacks}
	builtins[ListEquality] = nil
	builtins[ObjectEquality] = nil
	for _, name := range []string{"any.type", "any.tojson", "any.tostring", "any.in", "any.exists", "bool.and", "bool.or", "bool.not", "number.round", "number.ceil", "number.floor", "string.len", "string.contains", "string.matches", "string.startswith", "string.endswith", "string.lower", "string.upper", "string.timestamp", "string.duration", "string.bool", "string.number", "string.fromjson", "string.concat", "bytes.len", "list.size", "list.has", "list.contains", "list.unique", "list.sort", "list.concat", "list.any", "list.all", "list.map", "list.flatmap", "list.foldl", "list.foldr", "object.size", "object.has", "object.get", "object.keys", "object.values", "object.items", "timestamp.year", "timestamp.month", "timestamp.dayofmonth", "timestamp.dayofyear", "timestamp.hours", "timestamp.minutes", "timestamp.seconds", "timestamp.milliseconds", "timestamp.string", "timestamp.after", "timestamp.before", "timestamp.add", "duration.hours", "duration.minutes", "duration.seconds", "duration.milliseconds", "duration.microseconds", "duration.nanoseconds"} {
		builtins[Method(name)] = nil
	}
	for _, name := range []string{"list.any", "list.all", "list.map", "list.flatmap", "list.foldl", "list.foldr"} {
		builtins[Method(name)] = []ID{Callbacks}
	}
	for _, name := range []string{"string.frombase64", "bytes.tobase64"} {
		builtins[Method(name)] = nil
	}
}

// Set's zero value means unspecified. None is an explicitly empty policy.
type Set struct {
	specified bool
	ids       map[ID]bool
}

func None() Set { return Set{specified: true, ids: map[ID]bool{}} }
func Standard() Set {
	s := None()
	for id := range builtins {
		s.ids[id] = true
	}
	return s
}
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
	return out
}
func (s Set) Allow(ids ...ID) Set {
	out := s.clone()
	for _, id := range ids {
		for _, item := range expand(id) {
			out.ids[item] = true
		}
	}
	return out
}
func (s Set) Without(ids ...ID) Set {
	out := s.clone()
	for _, id := range ids {
		for _, item := range expand(id) {
			delete(out.ids, item)
		}
	}
	return out
}
func (s Set) Has(id ID) bool {
	for _, item := range expand(id) {
		if !s.ids[item] {
			return false
		}
	}
	return true
}
func (s Set) IDs() []ID {
	out := make([]ID, 0, len(s.ids))
	for id := range s.ids {
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Check validates IDs and dependencies, without implicitly enabling permissions.
func (s Set) Check() error { return s.CheckWithMethods() }

// CheckWithMethods recognizes explicitly declared extension IDs without adding
// permissions. Unknown IDs remain configuration errors.
func (s Set) CheckWithMethods(methodIDs ...string) error {
	extensions := make(map[ID]bool, len(methodIDs))
	for _, id := range methodIDs {
		extensions[Method(id)] = true
	}
	if !s.specified {
		return fmt.Errorf("capabilities must be explicitly selected")
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
