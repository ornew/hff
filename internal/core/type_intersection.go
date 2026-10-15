package core

// UnionTypes joins inferred alternatives, discarding Never and absorbing Any.
// NewUnionType retains literal schema alternatives; inference uses this
// normalization only after representation-sensitive checks have run.
func UnionTypes(types ...Type) Type {
	var values []Type
	for _, t := range types {
		if t.Kind() == AnyType {
			return t
		}
		if t.Kind() == UnionType {
			values = append(values, t.Members()...)
		} else if t.Kind() != NeverType {
			values = append(values, t)
		}
	}
	filtered := values[:0]
	for _, t := range values {
		if t.Kind() == AnyType {
			return t
		}
		if t.Kind() != NeverType {
			filtered = append(filtered, t)
		}
	}
	return NewUnionType(filtered...)
}

// IntersectTypes preserves schema information guaranteed by both contracts.
// Empty collections remain possible when element contracts are disjoint.
// Callers handling a nonempty positional value must check its slots as well.
func IntersectTypes(actual, target Type) Type {
	if actual.Kind() == AnyType {
		return target
	}
	if target.Kind() == AnyType {
		return actual
	}
	branches := func(t Type) []Type {
		if t.Kind() == UnionType {
			return t.Members()
		}
		return []Type{t}
	}
	var results []Type
	for _, a := range branches(actual) {
		for _, t := range branches(target) {
			// Literal schemas retain Any inside unions such as Any|null.
			// It remains the universal type at this branch, not a concrete
			// kind that can only intersect another Any branch.
			if a.Kind() == AnyType {
				results = append(results, t)
				continue
			}
			if t.Kind() == AnyType {
				results = append(results, a)
				continue
			}
			if a.Kind() == ListType && t.Kind() == ListType {
				results = append(results, NewListType(IntersectTypes(a.Element(), t.Element())))
				continue
			}
			if (a.Kind() == MapType || a.Kind() == StructType) && t.Kind() == MapType {
				if a.Kind() == MapType {
					results = append(results, NewMapType(IntersectTypes(a.Element(), t.Element())))
					continue
				}
				var fields []Field
				valid := true
				for _, field := range a.Fields() {
					field.Optional = field.Optional || intersectionPermitsNull(field.Type)
					field.Type = IntersectTypes(field.Type, t.Element())
					if field.Type.Kind() == NeverType && !field.Optional {
						valid = false
						break
					}
					fields = append(fields, field)
				}
				if valid {
					results = append(results, NewStructType(fields...))
				}
				continue
			}
			if a.Kind() == t.Kind() {
				results = append(results, a)
			}
		}
	}
	return UnionTypes(results...)
}

func intersectionPermitsNull(t Type) bool {
	if t.Kind() == AnyType || t.Kind() == NullType {
		return true
	}
	for _, member := range t.Members() {
		if member.Kind() == NullType {
			return true
		}
	}
	return false
}
