package hff

import "github.com/ornew/hff/internal/core"

// NullPolicy controls extension invocation when a receiver or argument is null.
type NullPolicy = core.NullPolicy

const (
	// PropagateNull skips an extension invocation when its receiver or an argument is null.
	PropagateNull = core.PropagateNull
	// InvokeNull allows dispatch when the declared receiver/argument type admits null.
	InvokeNull = core.InvokeNull
)

// MethodConfig declares one extension signature. Parameters are required and
// precede Optional parameters; Variadic, when valid, repeats after both lists.
// A zero Variadic means there is no repeated parameter. BaseCost must be
// positive and is charged for each local invocation.
type MethodConfig struct {
	// ID is the stable capability ID suffix, for example "service.lookup.v1".
	ID string
	// Name is the ASCII method identifier used in query calls.
	Name string
	// Receiver is the type accepted by this implementation.
	Receiver Type
	// Parameters are required positional argument types.
	Parameters []Type
	// Optional are trailing positional argument types; callers cannot skip them.
	Optional []Type
	// Variadic is the repeated trailing type, or the zero Type when absent.
	Variadic Type
	// Result is the successful result type. Null is always allowed as data failure.
	Result Type
	// NullPolicy controls invocation when receiver or arguments are null. The
	// zero value, PropagateNull, skips invocation and returns null.
	NullPolicy NullPolicy
	// BaseCost is the positive local step charge applied before each call.
	BaseCost int
}

// MethodDeclaration is the immutable, backend-independent extension signature.
// Its zero value is invalid. Engine configuration installs declarations;
// backend Build configuration separately installs implementations.
type MethodDeclaration core.MethodDeclaration

// NewMethodDeclaration validates and copies a method signature declaration.
// Invalid types, IDs, names, parameter ordering, or costs return a typed
// ConfigurationError.
func NewMethodDeclaration(c MethodConfig) (MethodDeclaration, error) {
	d, err := core.NewMethodDeclaration(core.MethodConfig{ID: c.ID, Name: c.Name, Receiver: core.Type(c.Receiver), Parameters: internalTypes(c.Parameters), Optional: internalTypes(c.Optional), Variadic: core.Type(c.Variadic), Result: core.Type(c.Result), NullPolicy: c.NullPolicy, BaseCost: c.BaseCost})
	if err != nil {
		return MethodDeclaration{}, &Error{Code: ConfigurationError, Message: err.Error()}
	}
	return MethodDeclaration(d), nil
}

// Valid reports whether d is a valid declaration rather than its zero value.
func (d MethodDeclaration) Valid() bool { return core.MethodDeclaration(d).Valid() }

// ID returns the stable signature ID used for capability checks and dispatch.
func (d MethodDeclaration) ID() string { return core.MethodDeclaration(d).ID() }

// Name returns the HFF call name, without its type-family prefix.
func (d MethodDeclaration) Name() string { return core.MethodDeclaration(d).Name() }

// Receiver returns a copy of the receiver type.
func (d MethodDeclaration) Receiver() Type { return Type(core.MethodDeclaration(d).Receiver()) }

// Result returns a copy of the successful result type.
func (d MethodDeclaration) Result() Type { return Type(core.MethodDeclaration(d).Result()) }

// NullPolicy returns the configured null dispatch policy.
func (d MethodDeclaration) NullPolicy() NullPolicy { return core.MethodDeclaration(d).NullPolicy() }

// BaseCost returns the positive per-invocation local step charge.
func (d MethodDeclaration) BaseCost() int { return core.MethodDeclaration(d).BaseCost() }

// Parameters returns a defensive copy of the required parameter types.
func (d MethodDeclaration) Parameters() []Type {
	return publicTypes(core.MethodDeclaration(d).Parameters())
}

// Optional returns a defensive copy of the trailing optional parameter types.
func (d MethodDeclaration) Optional() []Type {
	return publicTypes(core.MethodDeclaration(d).Optional())
}

// Variadic returns the repeated trailing parameter type, or an invalid Type.
func (d MethodDeclaration) Variadic() Type { return Type(core.MethodDeclaration(d).Variadic()) }

// MinArgs returns the minimum number of positional arguments.
func (d MethodDeclaration) MinArgs() int { return core.MethodDeclaration(d).MinArgs() }

// MaxArgs returns the maximum argument count, or -1 for a variadic signature.
func (d MethodDeclaration) MaxArgs() int { return core.MethodDeclaration(d).MaxArgs() }

// Parameter returns the type for argument index i, including optional and
// variadic positions. It reports false for a negative or out-of-range index.
func (d MethodDeclaration) Parameter(i int) (Type, bool) {
	t, ok := core.MethodDeclaration(d).Parameter(i)
	return Type(t), ok
}

// Equal reports whether two declarations have identical signature contracts.
func (d MethodDeclaration) Equal(other MethodDeclaration) bool {
	return core.MethodDeclaration(d).Equal(core.MethodDeclaration(other))
}
