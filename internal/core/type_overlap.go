package core

// TypesOverlap reports whether a value can satisfy both contracts. Empty
// collections overlap even when their element types do not.
func TypesOverlap(a, b Type) bool {
	if a.Kind() == NeverType || b.Kind() == NeverType || a.Kind() == InvalidType || b.Kind() == InvalidType {
		return false
	}
	if a.Kind() == AnyType || b.Kind() == AnyType {
		return true
	}
	if a.Kind() == UnionType {
		for _, m := range a.Members() {
			if TypesOverlap(m, b) {
				return true
			}
		}
		return false
	}
	if b.Kind() == UnionType {
		return TypesOverlap(b, a)
	}
	if a.Kind() == StructType && b.Kind() == MapType {
		return structMapOverlap(a, b)
	}
	if b.Kind() == StructType && a.Kind() == MapType {
		return structMapOverlap(b, a)
	}
	if a.Kind() != b.Kind() {
		return false
	}
	if a.Kind() != StructType {
		return true
	}
	af, bf := a.Fields(), b.Fields()
	am, bm := map[string]Field{}, map[string]Field{}
	for _, f := range af {
		am[f.Name] = f
	}
	for _, f := range bf {
		bm[f.Name] = f
	}
	for _, f := range af {
		other, ok := bm[f.Name]
		if !ok {
			if !f.Optional && !admitsNull(f.Type) {
				return false
			}
			continue
		}
		required := !f.Optional && !admitsNull(f.Type) || !other.Optional && !admitsNull(other.Type)
		if required && !TypesOverlap(f.Type, other.Type) {
			return false
		}
	}
	for _, f := range bf {
		if _, ok := am[f.Name]; !ok && !f.Optional && !admitsNull(f.Type) {
			return false
		}
	}
	return true
}
func structMapOverlap(s, m Type) bool {
	for _, f := range s.Fields() {
		if !f.Optional && !admitsNull(f.Type) && !TypesOverlap(f.Type, m.Element()) {
			return false
		}
	}
	return true
}

func MethodsOverlap(a, b MethodDeclaration) bool {
	if a.Name() != b.Name() || !TypesOverlap(a.Receiver(), b.Receiver()) {
		return false
	}
	n := max(a.MinArgs(), b.MinArgs())
	if a.MaxArgs() >= 0 && n > a.MaxArgs() || b.MaxArgs() >= 0 && n > b.MaxArgs() {
		return false
	}
	for i := 0; i < n; i++ {
		x, _ := a.Parameter(i)
		y, _ := b.Parameter(i)
		if !TypesOverlap(x, y) {
			return false
		}
	}
	return true
}
