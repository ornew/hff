package hff

import "github.com/ornew/hff/internal/core"

// NullPolicy controls whether an extension receives ordinary null values.
type NullPolicy = core.NullPolicy

const (
	PropagateNull = core.PropagateNull
	InvokeNull    = core.InvokeNull
)

// MethodConfig declares one signature. Parameters are required, Optional are
// trailing optional parameters, and Variadic is the repeated trailing type.
// A zero Variadic means no repeated parameter. BaseCost must be at least one.
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

// MethodDeclaration is an immutable extension contract, independent of backends.
// Its zero value is invalid. Implementations are registered separately at Build.
type MethodDeclaration core.MethodDeclaration

func NewMethodDeclaration(c MethodConfig) (MethodDeclaration, error) {
	d, err := core.NewMethodDeclaration(core.MethodConfig{ID: c.ID, Name: c.Name, Receiver: core.Type(c.Receiver), Parameters: internalTypes(c.Parameters), Optional: internalTypes(c.Optional), Variadic: core.Type(c.Variadic), Result: core.Type(c.Result), NullPolicy: c.NullPolicy, BaseCost: c.BaseCost})
	if err != nil {
		return MethodDeclaration{}, &Error{Code: ConfigurationError, Message: err.Error()}
	}
	return MethodDeclaration(d), nil
}
func (d MethodDeclaration) Valid() bool            { return core.MethodDeclaration(d).Valid() }
func (d MethodDeclaration) ID() string             { return core.MethodDeclaration(d).ID() }
func (d MethodDeclaration) Name() string           { return core.MethodDeclaration(d).Name() }
func (d MethodDeclaration) Receiver() Type         { return Type(core.MethodDeclaration(d).Receiver()) }
func (d MethodDeclaration) Result() Type           { return Type(core.MethodDeclaration(d).Result()) }
func (d MethodDeclaration) NullPolicy() NullPolicy { return core.MethodDeclaration(d).NullPolicy() }
func (d MethodDeclaration) BaseCost() int          { return core.MethodDeclaration(d).BaseCost() }
func (d MethodDeclaration) Parameters() []Type {
	return publicTypes(core.MethodDeclaration(d).Parameters())
}
func (d MethodDeclaration) Optional() []Type {
	return publicTypes(core.MethodDeclaration(d).Optional())
}
func (d MethodDeclaration) Variadic() Type { return Type(core.MethodDeclaration(d).Variadic()) }
func (d MethodDeclaration) MinArgs() int   { return core.MethodDeclaration(d).MinArgs() }
func (d MethodDeclaration) MaxArgs() int   { return core.MethodDeclaration(d).MaxArgs() }
func (d MethodDeclaration) Parameter(i int) (Type, bool) {
	t, ok := core.MethodDeclaration(d).Parameter(i)
	return Type(t), ok
}
func (d MethodDeclaration) Equal(other MethodDeclaration) bool {
	return core.MethodDeclaration(d).Equal(core.MethodDeclaration(other))
}
