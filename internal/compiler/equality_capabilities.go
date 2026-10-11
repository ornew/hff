package compiler

import (
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/internal/core"
)

// Inspect possible value pairs, including nested contents. Membership and
// uniqueness compare elements, so a scalar list does not itself need permission
// for list-content equality unless the whole list is an operand.
func (c *compiler) equalityCapabilities(left, right core.Type, source any) error {
	if c.config.Capabilities.Has(capabilities.StructuralEquality) {
		return nil
	}
	// A compiler belongs to one Compile call. Reuse storage, but never reuse
	// visited results: every check retains its own work and cancellation budget.
	if c.equalitySeen == nil {
		c.equalitySeen = make(map[[2]core.Type]bool)
	} else {
		clear(c.equalitySeen)
	}
	seen := c.equalitySeen
	var visit func(core.Type, core.Type) error
	visit = func(l, r core.Type) error {
		pair := [2]core.Type{l, r}
		if seen[pair] {
			return nil
		}
		seen[pair] = true
		if err := c.chargeCompile(source); err != nil {
			return err
		}
		if l.Kind() == core.UnionType {
			for _, member := range l.Members() {
				if err := visit(member, r); err != nil {
					return err
				}
			}
			return nil
		}
		if r.Kind() == core.UnionType {
			return visit(r, l)
		}
		if l.Kind() == core.AnyType && r.Kind() == core.AnyType {
			if err := c.allow(capabilities.ListEquality, source); err != nil {
				return err
			}
			return c.allow(capabilities.ObjectEquality, source)
		}
		if r.Kind() == core.AnyType {
			return visit(r, l)
		}
		if r.Kind() == core.ListType && (l.Kind() == core.ListType || l.Kind() == core.AnyType) {
			if err := c.allow(capabilities.ListEquality, source); err != nil {
				return err
			}
			item := l
			if l.Kind() == core.ListType {
				item = l.Element()
			}
			return visit(item, r.Element())
		}
		if !isObject(r) || (!isObject(l) && l.Kind() != core.AnyType) {
			return nil
		}
		if err := c.allow(capabilities.ObjectEquality, source); err != nil {
			return err
		}
		if l.Kind() == core.AnyType {
			if r.Kind() == core.MapType {
				return visit(l, r.Element())
			}
			for i := 0; i < r.FieldCount(); i++ {
				field, _ := r.FieldAt(i)
				if err := c.chargeCompile(source); err != nil {
					return err
				}
				if err := visit(l, field.Type); err != nil {
					return err
				}
			}
			return nil
		}
		if l.Kind() == core.MapType && r.Kind() == core.MapType {
			return visit(l.Element(), r.Element())
		}
		if r.Kind() == core.MapType {
			return visit(r, l)
		}
		if l.Kind() == core.MapType {
			for i := 0; i < r.FieldCount(); i++ {
				field, _ := r.FieldAt(i)
				if err := c.chargeCompile(source); err != nil {
					return err
				}
				if err := visit(l.Element(), field.Type); err != nil {
					return err
				}
			}
			return nil
		}
		// Struct fields are sorted. Only values under a shared key can be compared.
		for i, j := 0, 0; i < l.FieldCount() && j < r.FieldCount(); {
			if err := c.chargeCompile(source); err != nil {
				return err
			}
			lf, _ := l.FieldAt(i)
			rf, _ := r.FieldAt(j)
			switch {
			case lf.Name < rf.Name:
				i++
			case lf.Name > rf.Name:
				j++
			default:
				if err := visit(lf.Type, rf.Type); err != nil {
					return err
				}
				i++
				j++
			}
		}
		return nil
	}
	return visit(left, right)
}
