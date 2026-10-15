//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// Compute expected serialization from explicit Values, independently of the
// Local serializer and SQL lowering. Keys sort by Unicode code-point order.
func itemRoleOutputs(t testing.TB, object hff.Value) map[string]hff.Value {
	result := map[string]hff.Value{}
	for _, name := range []string{"items_json", "first_json", "keys_json", "filtered_json", "found_json", "values_json"} {
		result[name] = hff.String("null")
	}
	values, ok := object.AsObject()
	if !ok {
		return result
	}
	var keys []string
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var items, keyValues, vals, filtered []hff.Value
	for _, key := range keys {
		pair := hff.List(hff.String(key), values[key])
		items = append(items, pair)
		keyValues = append(keyValues, hff.String(key))
		vals = append(vals, values[key])
		if key == "app" {
			filtered = append(filtered, pair)
			result["found_json"] = hff.String(taggedCollectionJSON(t, pair))
		}
	}
	if len(items) > 0 {
		result["first_json"] = hff.String(taggedCollectionJSON(t, items[0]))
	}
	result["items_json"] = hff.String(taggedCollectionJSON(t, hff.List(items...)))
	result["keys_json"] = hff.String(taggedCollectionJSON(t, hff.List(keyValues...)))
	result["values_json"] = hff.String(taggedCollectionJSON(t, hff.List(vals...)))
	result["filtered_json"] = hff.String(taggedCollectionJSON(t, hff.List(filtered...)))
	return result
}

func TestPostgreSQLJSONItemRolesMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemRoleSources)
}

func TestPostgreSQLJSONItemTransformGuardPropagation(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemTransformGuardSources)
}

func TestPostgreSQLJSONItemFoldsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemFoldSources)
}

func TestPostgreSQLJSONItemUniquenessMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemUniqueSources)
}

func TestPostgreSQLJSONItemKeysMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemKeySources)
}

func TestPostgreSQLJSONItemGuardsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemGuardSources)
}

func TestPostgreSQLJSONItemSlotsMatchLocal(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemSlotSources, itemSlotFixtures()...)
}

func TestPostgreSQLJSONItemKeysWithoutMetadata(t *testing.T) {
	runPostgreSQLJSONItemRoleSources(t, itemKeyOnlySources, keyOnlyValueFixtures()...)
}

func runPostgreSQLJSONItemRoleSources(t *testing.T, sources func(itemRoleFixture) []string, fixtures ...itemRoleFixture) {
	t.Helper()
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	if len(fixtures) == 0 {
		fixtures = itemRoleFixtures()
	}
	for _, tc := range fixtures {
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				e, err := engine.New(engine.WithSchema(itemRoleSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
				if err != nil {
					t.Fatal(err)
				}
				obj := func(a, z hff.Value) hff.Value {
					fields := map[string]hff.Value{}
					if a.Kind() != hff.InvalidKind {
						if tc.mixed {
							a = hff.String("auth")
						}
						fields["app"] = a
					}
					if z.Kind() != hff.InvalidKind {
						fields["z"] = z
					}
					return hff.Object(fields)
				}
				missing := hff.Value{}
				full := obj(tc.first, tc.second)
				pairs := [][2]hff.Value{
					{full, full}, {full, obj(tc.second, tc.first)},
					{obj(hff.Null(), tc.zero), obj(hff.Null(), tc.zero)},
					{hff.Object(nil), hff.Object(nil)}, {hff.Null(), hff.Null()}, {hff.Object(nil), hff.Null()},
					{missing, full}, {full, missing},
					{obj(tc.first, missing), obj(missing, tc.second)},
					{obj(missing, tc.first), obj(tc.first, missing)},
					{obj(tc.first, missing), obj(hff.Null(), missing)},
				}
				if strings.HasSuffix(tc.name, "/map") {
					pairs = append(pairs, [2]hff.Value{hff.Object(map[string]hff.Value{"YQ==": tc.first, "あ": tc.second, "": hff.Null()}), hff.Object(map[string]hff.Value{"YQ==": tc.first, "あ": tc.first, "": hff.Null()})})
				}
				if tc.typ.Kind() == hff.AnyType {
					pairs = append(pairs, [2]hff.Value{hff.Int(1), hff.String("app")}, [2]hff.Value{hff.String("app"), hff.Int(1)})
				}
				pairs = append(pairs, tc.extraPairs...)
				var script strings.Builder
				fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,labels %s,other %s,items_json text,first_json text,keys_json text,filtered_json text,found_json text,values_json text,labels_meta jsonb,other_meta jsonb);\n", format, format)
				records := make([]hff.Value, len(pairs))
				for i, pair := range pairs {
					fields := itemRoleOutputs(t, pair[0])
					parts := []string{fmt.Sprint(i), "NULL", "NULL"}
					for j, name := range []string{"labels", "other"} {
						if pair[j].Kind() != hff.InvalidKind {
							fields[name] = pair[j]
							parts[j+1] = sqlFixtureString(taggedCollectionJSON(t, pair[j]))
						}
					}
					for _, name := range []string{"items_json", "first_json", "keys_json", "filtered_json", "found_json", "values_json"} {
						parts = append(parts, valuesText(t, fields[name]))
					}
					for _, v := range pair {
						meta := `{"version":1,"types":{}}`
						if v.Kind() != hff.InvalidKind {
							meta = pairTypeMetadata(t, v)
						}
						parts = append(parts, sqlFixtureString(meta))
					}
					fmt.Fprintf(&script, "INSERT INTO records VALUES(%s);\n", strings.Join(parts, ","))
					records[i] = hff.Object(fields)
				}
				var expected []bool
				var labels []string
				for index, source := range sources(tc) {
					p, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, p)
					if err != nil {
						t.Fatal(source, err)
					}
					q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(itemRoleBindings(format, tc.metadata)...))
					if err != nil {
						t.Fatal(source, err)
					}
					appendAggregateProfileQuery(t, &script, q, "records ORDER BY id", index)
					for i, record := range records {
						want, err := filter.Match(ctx, record)
						if err != nil {
							t.Fatal(source, i, err)
						}
						expected = append(expected, want)
						labels = append(labels, fmt.Sprintf("%s row%d", source, i))
					}
				}
				t.Logf("%d query-record results", len(expected))
				runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
			})
		}
	}
}

func TestPostgreSQLItemPairsPreserveDifferentValueFamilies(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	var families []itemRoleFixture
	for _, tc := range itemRoleFixtures() {
		if strings.HasSuffix(tc.name, "/map") {
			families = append(families, tc)
		}
	}
	for i, left := range families {
		for j, right := range families {
			if i == j {
				continue
			}
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("left", left.typ), schema.Field("right", right.typ))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			for _, source := range []string{
				`left:items=right:items`, `left:items!=right:items`, `left:items[0]=right:items[0]`, `left:items:contains(right:items[0])`,
				`left:items:[list[list[string|null]]]:concat(right:items:[list[list[string|null]]]):json:exists`,
				`left:items:[list[list[string|` + left.tag + `|null]]]:concat(right:items:[list[list[string|null]]]):map(p=>p[1]:type):json:exists`,
				`{"app":"auth","z":left.app}:items[0]:[list[string]][1]:len=4`,
				`{"app":"auth","z":left.app}:items[1]:[list[string]][1]:exists`,
			} {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
					q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"left"}, hffsql.Column("records", "lhs"), format), hffsql.BindJSON(hff.Path{"right"}, hffsql.Column("records", "rhs"), format)))
					if err != nil {
						t.Fatal(source, err)
					}
					obj := func(v hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"app": v}) }
					for _, pair := range [][2]hff.Value{{obj(left.first), obj(right.first)}, {obj(hff.Null()), obj(hff.Null())}, {hff.Object(nil), hff.Object(nil)}, {hff.Null(), hff.Null()}} {
						want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"left": pair[0], "right": pair[1]}))
						if err != nil {
							t.Fatal(err)
						}
						from := fmt.Sprintf("(VALUES (%s::%s,%s::%s)) records(lhs,rhs)", sqlFixtureString(taggedCollectionJSON(t, pair[0])), format, sqlFixtureString(taggedCollectionJSON(t, pair[1])), format)
						appendAggregateProfileQuery(t, &script, q, from, len(expected))
						expected = append(expected, want)
						labels = append(labels, left.tag+"/"+right.tag+"/"+source)
					}
				}
			}
		}
	}
	t.Logf("%d query-record results", len(expected))
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLCallbacksExcludeNonListUnionBranches(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(callbackUnionSchema()), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	values := []hff.Value{
		hff.List(hff.Bytes([]byte("a"))),
		hff.List(hff.Bytes([]byte("b")), hff.Null(), hff.Bytes([]byte("a"))),
		hff.List(), hff.Object(map[string]hff.Value{"key": hff.String("YQ==")}),
		hff.Object(nil), hff.Null(),
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			var script strings.Builder
			fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,data %s);\n", format)
			for i, value := range values {
				fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s);\n", i, sqlFixtureString(taggedCollectionJSON(t, value)))
			}
			var expected []bool
			var labels []string
			for i, source := range callbackUnionSources() {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(source, err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)))
				if err != nil {
					t.Fatal(source, err)
				}
				appendAggregateProfileQuery(t, &script, q, "records ORDER BY id", i)
				for row, value := range values {
					want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": value}))
					if err != nil {
						t.Fatal(source, row, err)
					}
					expected = append(expected, want)
					labels = append(labels, fmt.Sprintf("%s row%d", source, row))
				}
			}
			t.Logf("%d query-record results", len(expected))
			runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
		})
	}
}
