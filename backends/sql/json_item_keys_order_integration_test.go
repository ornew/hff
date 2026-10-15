//go:build integration

package sql_test

import (
	"testing"

	"github.com/ornew/hff"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLJSONItemKeysKeepOrderAndFirstFailure(t *testing.T) {
	object := func(keys ...string) hff.Value {
		fields := make(map[string]hff.Value, len(keys))
		for _, key := range keys {
			fields[key] = hff.Bool(true)
		}
		return hff.Object(fields)
	}
	fixture := itemRoleFixture{
		name: "encoded/key-order", typ: schema.NullableType(schema.MapOf(schema.NullableType(schema.BoolType))),
		first: hff.Bool(true), second: hff.Bool(false), zero: hff.Bool(false),
		extraPairs: [][2]hff.Value{
			{object("[3]", "[1,2]"), hff.Object(nil)},
			{object("bad", "1"), hff.Object(nil)},
			{object("bad", "[1]"), hff.Object(nil)},
			{object("1", ""), hff.Object(nil)},
			{object("1.0", "1"), hff.Object(nil)},
			{object("0", "-0.0"), hff.Object(nil)},
		},
	}
	sources := func(itemRoleFixture) []string {
		return []string{
			`labels:items:map(p=>p[0]):json=keys_json`,
			`labels:items:flatmap(p=>p[0]:fromjson!):exists`,
			`labels:items:flatmap(p=>p[0]:fromjson!)=null`,
			`labels:items:flatmap(p=>p[0]:fromjson!):json="[1,2,3]"`,
			`labels:items:flatmap(p=>p[0]:fromjson!):json="null"`,
			`labels:items:flatmap(p=>[p[0]:fromjson!]):exists`,
			`labels:items:map(p=>p[0]:fromjson):json!:exists`,
			`labels:items:min(p=>p[0]:number):type="int"`,
			`labels:items:min(p=>p[0]:number):type="float"`,
		}
	}
	runPostgreSQLJSONItemRoleSources(t, sources, fixture)
}
