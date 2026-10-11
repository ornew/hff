package core

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

// NullPolicy controls whether a declared method observes ordinary null.
type NullPolicy uint8

const (
	PropagateNull NullPolicy = iota
	InvokeNull
)

type MethodConfig struct {
	ID, Name   string
	Receiver   Type
	Parameters []Type
	Optional   []Type
	Variadic   Type
	Result     Type
	NullPolicy NullPolicy
	BaseCost   int
}

// MethodDeclaration owns a single immutable signature and its execution contract.
type MethodDeclaration struct{ data *methodData }
type methodData struct{ config MethodConfig }

func NewMethodDeclaration(c MethodConfig) (MethodDeclaration, error) {
	if c.ID == "" || !utf8.ValidString(c.ID) || strings.ContainsAny(c.ID, "\x00\r\n") {
		return MethodDeclaration{}, fmt.Errorf("invalid method ID")
	}
	if !methodName(c.Name) {
		return MethodDeclaration{}, fmt.Errorf("method name must be an ASCII identifier")
	}
	if c.BaseCost < 1 {
		return MethodDeclaration{}, fmt.Errorf("method base cost must be at least one")
	}
	if c.NullPolicy != PropagateNull && c.NullPolicy != InvokeNull {
		return MethodDeclaration{}, fmt.Errorf("invalid method null policy")
	}
	types := append([]Type{c.Receiver, c.Result}, c.Parameters...)
	types = append(types, c.Optional...)
	if c.Variadic.Kind() != InvalidType {
		types = append(types, c.Variadic)
	}
	for _, t := range types {
		if err := t.Check(); err != nil {
			return MethodDeclaration{}, err
		}
	}
	c.Parameters = append([]Type(nil), c.Parameters...)
	c.Optional = append([]Type(nil), c.Optional...)
	return MethodDeclaration{&methodData{c}}, nil
}
func methodName(s string) bool {
	if s == "" {
		return false
	}
	for i, ch := range []byte(s) {
		if !(ch == '_' || ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || i > 0 && ch >= '0' && ch <= '9') {
			return false
		}
	}
	return true
}
func (d MethodDeclaration) Valid() bool { return d.data != nil }
func (d MethodDeclaration) ID() string {
	if !d.Valid() {
		return ""
	}
	return d.data.config.ID
}
func (d MethodDeclaration) Name() string {
	if !d.Valid() {
		return ""
	}
	return d.data.config.Name
}
func (d MethodDeclaration) Receiver() Type {
	if !d.Valid() {
		return Type{}
	}
	return d.data.config.Receiver
}
func (d MethodDeclaration) Result() Type {
	if !d.Valid() {
		return Type{}
	}
	return d.data.config.Result
}
func (d MethodDeclaration) NullPolicy() NullPolicy {
	if !d.Valid() {
		return PropagateNull
	}
	return d.data.config.NullPolicy
}
func (d MethodDeclaration) BaseCost() int {
	if !d.Valid() {
		return 0
	}
	return d.data.config.BaseCost
}
func (d MethodDeclaration) Parameters() []Type {
	if !d.Valid() {
		return nil
	}
	return append([]Type(nil), d.data.config.Parameters...)
}
func (d MethodDeclaration) Optional() []Type {
	if !d.Valid() {
		return nil
	}
	return append([]Type(nil), d.data.config.Optional...)
}
func (d MethodDeclaration) Variadic() Type {
	if !d.Valid() {
		return Type{}
	}
	return d.data.config.Variadic
}
func (d MethodDeclaration) MinArgs() int {
	if !d.Valid() {
		return 0
	}
	return len(d.data.config.Parameters)
}
func (d MethodDeclaration) MaxArgs() int {
	if d.Variadic().Kind() != InvalidType {
		return -1
	}
	if !d.Valid() {
		return 0
	}
	return len(d.data.config.Parameters) + len(d.data.config.Optional)
}
func (d MethodDeclaration) Parameter(i int) (Type, bool) {
	if !d.Valid() || i < 0 {
		return Type{}, false
	}
	c := d.data.config
	if i < len(c.Parameters) {
		return c.Parameters[i], true
	}
	i -= len(c.Parameters)
	if i < len(c.Optional) {
		return c.Optional[i], true
	}
	if c.Variadic.Kind() != InvalidType {
		return c.Variadic, true
	}
	return Type{}, false
}
func (d MethodDeclaration) Equal(other MethodDeclaration) bool {
	if !d.Valid() || !other.Valid() {
		return false
	}
	if d.ID() != other.ID() || d.Name() != other.Name() || !d.Receiver().Equal(other.Receiver()) || !d.Result().Equal(other.Result()) || d.NullPolicy() != other.NullPolicy() || d.BaseCost() != other.BaseCost() || d.MinArgs() != other.MinArgs() || d.MaxArgs() != other.MaxArgs() {
		return false
	}
	a, b := d.data.config, other.data.config
	if len(a.Optional) != len(b.Optional) || !a.Variadic.Equal(b.Variadic) {
		return false
	}
	for i := 0; i < len(a.Parameters)+len(a.Optional); i++ {
		x, _ := d.Parameter(i)
		y, _ := other.Parameter(i)
		if !x.Equal(y) {
			return false
		}
	}
	return true
}
