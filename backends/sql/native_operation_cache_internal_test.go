package sql

import (
	"context"
	"reflect"
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestNativeOperationHitRetainsNonliteralArguments(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Int("i"), schema.Int("j"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `i+j>0`)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := p.Root().Child(0)
	l, _ := n.Child(0)
	r, _ := n.Child(1)
	left := operationOperand{l, expression{"i", "TRUE", hff.IntType}, 0}
	right := operationOperand{r, expression{"j", "TRUE", hff.IntType}, 0}
	b := &builder{ctx: ctx}
	if _, hit := b.reuseNativeOperation(n, "int-+", 0, left, right); hit {
		t.Fatal("first operation unexpectedly reused a result")
	}
	key := b.operationKeys[n]
	b.rememberPath(key, expression{"existing.v", "TRUE", hff.IntType})
	delete(b.operationKeys, n)
	// A future lowering may return an existing carrier while emitting another
	// parameter-dependent step. An exact carrier match cannot discard it.
	b.args = []any{int64(123)}
	b.steps = []string{"(SELECT $1::bigint AS v OFFSET 0) AS new_operand"}
	left.argumentsAdded = 1
	if _, hit := b.reuseNativeOperation(n, "int-+", 0, left, right); hit {
		t.Fatal("nonliteral argument-producing operand acquired a cache hit")
	}
	if !reflect.DeepEqual(b.args, []any{int64(123)}) || b.identityAliases[n] || len(b.operationKeys) != 0 {
		t.Fatal("miss discarded a live parameter or acquired a reuse marker", b.args, b.operationKeys)
	}
}
