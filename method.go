package hff

import "github.com/ornew/hff/internal/core"

// NullPolicy controls whether an extension callback runs when its receiver or
// an argument evaluates to explicit null. No-value always propagates without
// invoking an extension.
type NullPolicy = core.NullPolicy

const (
	// PropagateNull skips the extension invocation when the receiver or any
	// evaluated argument is null, returning null. This is the zero/default policy.
	PropagateNull = core.PropagateNull
	// InvokeNull permits dispatch for null receivers or arguments only when the
	// corresponding declared type admits NullType. It does not turn no-value into
	// a Value or invoke the callback for no-value.
	InvokeNull = core.InvokeNull
)

// MethodConfig declares one extension signature shared by engine compilation
// and backend registration. Parameters are required positional arguments,
// followed by trailing Optional arguments and then repeated Variadic
// arguments. Optional positions cannot be skipped to reach Variadic. A zero
// Variadic means no repeated argument. BaseCost must be positive and is
// charged for each local callback invocation; backends validate implementation
// registration separately.
type MethodConfig struct {
	// ID is the stable extension identity suffix, for example "service.lookup.v1".
	// Capabilities.Method(ID) creates its permission ID; keep ID stable across
	// engine and backend configuration.
	ID string
	// Name is the ASCII method identifier used in query calls. Receiver types may
	// distinguish overloads, but declarations must not create ambiguous overloads.
	Name string
	// Receiver is the type accepted by the implementation. Calls with an
	// incompatible receiver fail during compilation rather than being coerced.
	Receiver Type
	// Parameters are required positional argument types, in query argument
	// order. Values are type-checked; this declaration performs no conversion.
	Parameters []Type
	// Optional are trailing positional argument types; callers cannot skip them.
	Optional []Type
	// Variadic is the repeated trailing type, or the zero Type when absent.
	Variadic Type
	// Result is the declared result type. An ordinary Null result
	// is also always allowed as a data failure, regardless of this type.
	Result Type
	// NullPolicy controls invocation when receiver or arguments are null. The
	// zero value, PropagateNull, skips invocation and returns null.
	NullPolicy NullPolicy
	// BaseCost is the positive local step charge applied before each call.
	BaseCost int
}

// MethodDeclaration is the immutable, backend-independent extension signature.
// Its zero value is invalid. Install declarations in engine configuration to
// type-check calls and select their capabilities; register matching executable
// implementations separately with a backend Build configuration.
type MethodDeclaration core.MethodDeclaration

// NewMethodDeclaration validates and copies a method signature declaration.
// Invalid types, IDs, names, parameter ordering, null policy, or costs return a
// ConfigurationError. The returned declaration does not register an
// implementation or grant its capability.
func NewMethodDeclaration(c MethodConfig) (MethodDeclaration, error) {
	d, err := core.NewMethodDeclaration(core.MethodConfig{ID: c.ID, Name: c.Name, Receiver: core.Type(c.Receiver), Parameters: internalTypes(c.Parameters), Optional: internalTypes(c.Optional), Variadic: core.Type(c.Variadic), Result: core.Type(c.Result), NullPolicy: c.NullPolicy, BaseCost: c.BaseCost})
	if err != nil {
		return MethodDeclaration{}, &Error{Code: ConfigurationError, Message: err.Error()}
	}
	return MethodDeclaration(d), nil
}

// Valid reports whether d is a successfully constructed declaration rather
// than its zero value.
func (d MethodDeclaration) Valid() bool { return core.MethodDeclaration(d).Valid() }

// ID returns the stable extension ID used for capability checks and dispatch,
// or an empty string for an invalid zero declaration.
func (d MethodDeclaration) ID() string { return core.MethodDeclaration(d).ID() }

// Name returns the HFF call name, without its type-family prefix, or an empty
// string for an invalid zero declaration.
func (d MethodDeclaration) Name() string { return core.MethodDeclaration(d).Name() }

// Receiver returns the immutable receiver type descriptor by value. The zero
// declaration returns an invalid Type.
func (d MethodDeclaration) Receiver() Type { return Type(core.MethodDeclaration(d).Receiver()) }

// Result returns the successful result type descriptor. Null remains an
// allowed callback result even when this type is non-nullable.
func (d MethodDeclaration) Result() Type { return Type(core.MethodDeclaration(d).Result()) }

// NullPolicy returns the configured null dispatch policy. An invalid zero
// declaration reports PropagateNull, the zero/default policy.
func (d MethodDeclaration) NullPolicy() NullPolicy { return core.MethodDeclaration(d).NullPolicy() }

// BaseCost returns the positive per-invocation local step charge, or zero for
// an invalid declaration.
func (d MethodDeclaration) BaseCost() int { return core.MethodDeclaration(d).BaseCost() }

// Parameters returns a defensive copy of required positional parameter types.
func (d MethodDeclaration) Parameters() []Type {
	return publicTypes(core.MethodDeclaration(d).Parameters())
}

// Optional returns a defensive copy of trailing optional parameter types.
// These positions are positional and cannot be omitted while supplying a later
// variadic argument.
func (d MethodDeclaration) Optional() []Type {
	return publicTypes(core.MethodDeclaration(d).Optional())
}

// Variadic returns the repeated trailing parameter type, or an invalid Type.
func (d MethodDeclaration) Variadic() Type { return Type(core.MethodDeclaration(d).Variadic()) }

// MinArgs returns the minimum positional argument count, including all required
// parameters but not optional or variadic arguments. An invalid declaration
// returns zero.
func (d MethodDeclaration) MinArgs() int { return core.MethodDeclaration(d).MinArgs() }

// MaxArgs returns the maximum positional argument count, or -1 when Variadic
// makes the signature unbounded. An invalid declaration returns its zero
// value.
func (d MethodDeclaration) MaxArgs() int { return core.MethodDeclaration(d).MaxArgs() }

// Parameter returns the type accepted at zero-based argument position i,
// including optional and repeated variadic positions. The bool is false for a
// negative index, an out-of-range position, or an invalid declaration.
func (d MethodDeclaration) Parameter(i int) (Type, bool) {
	t, ok := core.MethodDeclaration(d).Parameter(i)
	return Type(t), ok
}

// Equal reports whether two valid declarations have identical IDs, types,
// argument positions, null policy, and base cost. It is useful when Build
// verifies that a registered implementation still matches the engine's
// declaration; invalid zero declarations compare false.
func (d MethodDeclaration) Equal(other MethodDeclaration) bool {
	return core.MethodDeclaration(d).Equal(core.MethodDeclaration(other))
}
