package engine_test

import (
	"context"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func declare(t *testing.T, id, name string, receiver hff.Type, params ...hff.Type) hff.MethodDeclaration {
	t.Helper()
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: id, Name: name, Receiver: receiver, Parameters: params, Result: schema.StringType, BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func extensionEngine(t *testing.T, methods []hff.MethodDeclaration, policy capabilities.Set) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"), schema.Any("y"))), engine.WithCapabilities(policy), engine.WithMethods(methods...))
	if err != nil {
		t.Fatal(err)
	}
	return e
}
func TestExtensionCompilationAndDiscovery(t *testing.T) {
	text := declare(t, "service.text.label.v1", "label", schema.StringType)
	integer := declare(t, "service.int.label.v1", "label", schema.IntType)
	policy := capabilities.Standard().Allow(capabilities.Method(text.ID()), capabilities.Method(integer.ID()))
	methods := []hff.MethodDeclaration{text, integer}
	option := engine.WithMethods(methods...)
	methods[0] = hff.MethodDeclaration{}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"))), engine.WithCapabilities(policy), option)
	if err != nil {
		t.Fatal(err)
	}
	owned := e.Methods()
	owned[0] = hff.MethodDeclaration{}
	if !e.Methods()[0].Equal(text) {
		t.Fatal("method config leaked")
	}
	p, err := e.Compile(context.Background(), `x:label:exists`)
	if err != nil {
		t.Fatal(err)
	}
	// Locate the declared call without assuming bool wrapping optimizations.
	var inspect func(hff.Node)
	found := 0
	inspect = func(n hff.Node) {
		if m, ok := n.Method(); ok && m.Name() == "label" {
			for _, s := range m.Signatures() {
				d, ok := s.Declaration()
				if !ok || d.ID() != s.ID() {
					t.Fatal("missing extension IR contract")
				}
				found++
			}
		}
		for i := 0; i < n.ChildCount(); i++ {
			child, _ := n.Child(i)
			inspect(child)
		}
	}
	inspect(p.Root())
	if found != 2 {
		t.Fatalf("resolved signatures %d", found)
	}
	narrowed := extensionEngine(t, []hff.MethodDeclaration{text, integer}, capabilities.Standard().Allow(capabilities.Method(text.ID())))
	_, err = narrowed.Compile(context.Background(), `x:label`)
	requireCode(t, err, hff.CapabilityError)
	_, err = narrowed.Compile(context.Background(), `x:[string]:label`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = narrowed.Compile(context.Background(), `x:[bytes]:label`)
	requireCode(t, err, hff.TypeError)
	_, err = narrowed.Compile(context.Background(), `x:label(1)`)
	requireCode(t, err, hff.TypeError)
	_, err = narrowed.Compile(context.Background(), `x:contains("a")`)
	if err != nil {
		t.Fatal(err)
	}
}
func TestExtensionRegistryRejectsAmbiguityAndUnknownPermissions(t *testing.T) {
	a := declare(t, "a", "custom", schema.StringType)
	for _, other := range []hff.MethodDeclaration{a, declare(t, "b", "custom", schema.StringType), declare(t, "b", "custom", schema.AnyType), declare(t, "b", "contains", schema.IntType), declare(t, "any.type", "custom2", schema.IntType), {}} {
		_, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"))), engine.WithCapabilities(capabilities.Standard()), engine.WithMethods(a, other))
		requireCode(t, err, hff.ConfigurationError)
	}
	_, err := engine.New(engine.WithSchema(schema.Struct(schema.Any("x"))), engine.WithCapabilities(capabilities.Standard().Allow(capabilities.Method("not.declared"))))
	requireCode(t, err, hff.ConfigurationError)
	// Empty lists satisfy both element contracts, so these signatures overlap.
	l1 := declare(t, "l1", "custom", schema.ListOf(schema.StringType))
	l2 := declare(t, "l2", "custom", schema.ListOf(schema.IntType))
	_, err = engine.New(engine.WithSchema(schema.Struct()), engine.WithCapabilities(capabilities.Standard()), engine.WithMethods(l1, l2))
	requireCode(t, err, hff.ConfigurationError)
	// Different required argument types separate otherwise identical receivers.
	extensionEngine(t, []hff.MethodDeclaration{declare(t, "s", "custom", schema.StringType, schema.StringType), declare(t, "i", "custom", schema.StringType, schema.IntType)}, capabilities.Standard())
}
func TestExtensionDeclarationOwnershipAndArity(t *testing.T) {
	params := []hff.Type{schema.StringType}
	optional := []hff.Type{schema.IntType}
	d, err := hff.NewMethodDeclaration(hff.MethodConfig{ID: "sample.v1", Name: "sample", Receiver: schema.AnyType, Parameters: params, Optional: optional, Variadic: schema.BoolType, Result: schema.StringType, BaseCost: 2})
	if err != nil {
		t.Fatal(err)
	}
	params[0] = schema.IntType
	optional[0] = schema.StringType
	got := d.Parameters()
	got[0] = schema.IntType
	if !d.Parameters()[0].Equal(schema.StringType) || !d.Optional()[0].Equal(schema.IntType) || d.MinArgs() != 1 || d.MaxArgs() != -1 {
		t.Fatal("declaration ownership or arity")
	}
	for i, want := range []hff.Type{schema.StringType, schema.IntType, schema.BoolType, schema.BoolType} {
		actual, ok := d.Parameter(i)
		if !ok || !actual.Equal(want) {
			t.Fatal("parameter contract")
		}
	}
	for _, c := range []hff.MethodConfig{{}, {ID: "a", Name: "x", Receiver: schema.IntType, Result: schema.IntType}, {ID: "a", Name: "bad-name", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1}, {ID: "a", Name: "x", Receiver: schema.IntType, Result: schema.IntType, BaseCost: 1, NullPolicy: 99}} {
		_, err := hff.NewMethodDeclaration(c)
		requireCode(t, err, hff.ConfigurationError)
	}
	e := extensionEngine(t, []hff.MethodDeclaration{d}, capabilities.Standard().Allow(capabilities.Method(d.ID())))
	for _, q := range []string{`x:sample("a")`, `x:sample("a",2)`, `x:sample("a",2,true,false)`} {
		if _, err := e.Compile(context.Background(), q); err != nil {
			t.Fatal(q, err)
		}
	}
	for _, q := range []string{`x:sample`, `x:sample("a",false)`, `x:sample("a",2,3)`} {
		_, err := e.Compile(context.Background(), q)
		requireCode(t, err, hff.TypeError)
	}
}
