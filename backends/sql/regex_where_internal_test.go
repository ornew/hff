package sql

import (
	"context"
	"errors"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/internal/core"
	"github.com/ornew/hff/internal/plan"
)

func TestLiteralRegexPatternValidatesUnboundOriginal(t *testing.T) {
	for _, text := range []string{"[\x00", "\xff"} {
		n := hff.Node(plan.NewNode(plan.Spec{Kind: plan.LiteralNode, Type: core.PrimitiveType(core.StringType), Literal: core.String(text)}))
		b := &builder{ctx: context.Background()}
		_, _, err := b.literalRegexPattern(n)
		var detail *hff.Error
		if !errors.As(err, &detail) || detail.Code != hff.BackendError || detail.Message != "PostgreSQL text cannot encode invalid UTF-8 or NUL" {
			t.Fatalf("%q bypassed original text validation: %v", text, err)
		}
		if len(b.args) != 0 {
			t.Fatal("pattern preparation unexpectedly bound a parameter")
		}
	}
}

func TestDirectRegexEligibilityExcludesCustomDeclarations(t *testing.T) {
	// Engine registration rejects this built-in name. Check the lowerer's
	// defensive boundary independently, without registering an invalid engine.
	declaration, err := core.NewMethodDeclaration(core.MethodConfig{ID: "service.matches", Name: "matches", Receiver: core.PrimitiveType(core.StringType), Parameters: []core.Type{core.PrimitiveType(core.StringType)}, Result: core.PrimitiveType(core.BoolType), BaseCost: 1})
	if err != nil {
		t.Fatal(err)
	}
	receiver := plan.NewNode(plan.Spec{Kind: plan.VariableNode, Type: core.PrimitiveType(core.StringType), Path: []string{"name"}})
	pattern := plan.NewNode(plan.Spec{Kind: plan.LiteralNode, Type: core.PrimitiveType(core.StringType), Literal: core.String("A")})
	n := hff.Node(plan.NewNode(plan.Spec{Kind: plan.CallNode, Type: core.PrimitiveType(core.BoolType), Children: []plan.Node{receiver, pattern}, Method: plan.NewMethod("matches", plan.NewExtensionSignature(declaration))}))
	b := &builder{ctx: context.Background(), bindings: map[string]string{pathKey([]string{"name"}): `"records"."name"`}}
	if b.directScalarRegexEligible(n) {
		t.Fatal("custom matches declaration bypassed extension dispatch")
	}
}
