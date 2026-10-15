package sql

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/ornew/hff"
)

func TestEmptyDeferredScopeRestoresBuilder(t *testing.T) {
	for _, failure := range []error{nil, errors.New("unsupported"), context.Canceled} {
		b := builder{
			ctx: context.Background(), alias: 12, args: []any{"kept"}, steps: []string{"outer"},
			parameters:   map[hff.BindingID]jsonParameter{1: {code: "outer.value"}},
			sharedPaths:  map[string]expression{"outer": {code: "kept"}},
			jsonValues:   map[string]JSONFormat{"outer.value": JSON},
			reserved:     map[string]bool{"outer": true},
			itemRoleMemo: &jsonItemRoleMemo{pairs: map[hff.Node]*jsonItemRoles{}, lists: map[hff.Node]*jsonItemRoles{}},
		}
		before := b.validationBuilder()
		memo := b.itemRoleMemo
		// This indirect closure captures the same pointer used by consumers.
		work := func() error {
			b.alias++
			b.args = append(b.args, "discarded")
			b.steps = append(b.steps, "discarded")
			b.parameters[99] = jsonParameter{code: "discarded"}
			b.sharedPaths = map[string]expression{"discarded": {code: "discarded"}}
			b.jsonValues["discarded"] = JSONB
			b.reserved["discarded"] = true
			b.itemRoleMemo = &jsonItemRoleMemo{}
			return failure
		}
		_, _, err := b.inDeferredArm(jsonDeferredArm{empty: true, steps: []string{"empty"}, parameters: map[hff.BindingID]jsonParameter{}}, hff.Node{}, work)
		if !errors.Is(err, failure) {
			t.Fatal(err, failure)
		}
		if b.itemRoleMemo != memo {
			t.Fatal("enclosing role memo replaced")
		}
		b.itemRoleMemo = nil
		if !reflect.DeepEqual(b, before) {
			t.Fatalf("empty validation leaked state after %v", failure)
		}
		if alias := b.nextAlias(); alias == "" || b.alias != 13 {
			t.Fatal("next sibling alias changed", alias, b.alias)
		}
		if len(b.args) != 1 || b.args[0] != "kept" {
			t.Fatal("next sibling sees discarded args", b.args)
		}
	}
}
