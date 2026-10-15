//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
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

// Keep the storage fixture independent from SQL and Local serialization. The
// duration seeds and endpoints have explicitly prescribed canonical spellings.
func taggedCollectionJSON(t testing.TB, v hff.Value) string {
	t.Helper()
	quote := func(s string) string { b, _ := json.Marshal(s); return string(b) }
	switch v.Kind() {
	case hff.DurationKind:
		d, _ := v.AsDuration()
		switch d {
		case 0:
			return `"0s"`
		case time.Nanosecond:
			return `"0.000000001s"`
		case 2 * time.Nanosecond:
			return `"0.000000002s"`
		case -time.Nanosecond:
			return `"-0.000000001s"`
		case time.Duration(math.MinInt64):
			return `"-2562047h47m16.854775808s"`
		case time.Duration(math.MaxInt64):
			return `"2562047h47m16.854775807s"`
		default:
			t.Fatalf("duration fixture not prescribed: %v", d)
		}
	case hff.TimestampKind:
		x, _ := v.AsTimestamp()
		return quote(x.UTC().Format(time.RFC3339Nano))
	case hff.ListKind:
		xs, _ := v.AsList()
		parts := make([]string, len(xs))
		for i, x := range xs {
			parts[i] = taggedCollectionJSON(t, x)
		}
		return "[" + strings.Join(parts, ",") + "]"
	case hff.ObjectKind:
		obj, _ := v.AsObject()
		keys := make([]string, 0, len(obj))
		for key := range obj {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		parts := make([]string, len(keys))
		for i, key := range keys {
			parts[i] = quote(key) + ":" + taggedCollectionJSON(t, obj[key])
		}
		return "{" + strings.Join(parts, ",") + "}"
	}
	return metadataJSONFixture(t, v)
}

func TestPostgreSQLTaggedCollectionUnionsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	for _, tc := range taggedCollectionCases() {
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(tc.name+"/"+string(format), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
				defer cancel()
				e, err := engine.New(engine.WithSchema(taggedCollectionSchema(tc)), engine.WithCapabilities(capabilities.Standard()))
				if err != nil {
					t.Fatal(err)
				}
				pairs := [][2]hff.Value{
					{tc.zero, tc.first}, {tc.first, tc.first}, {tc.first, tc.second},
					{tc.collection, tc.collection}, {tc.empty, tc.collection},
					{hff.Null(), hff.Null()}, {hff.Int(1), hff.Int(2)},
					{tc.first, tc.collection}, {tc.withNull, tc.empty},
					{hff.Value{}, tc.first}, {tc.collection, hff.Value{}},
					{hff.Int(1), hff.Int(2)}, {hff.Int(1), hff.Int(2)},
				}
				if tc.container == "list" {
					for _, index := range []int64{0, -1, 2147483648, -2147483649} {
						pairs = append(pairs, [2]hff.Value{tc.collection, hff.Int(index)})
					}
				}
				if tc.float {
					pairs = append(pairs, [2]hff.Value{hff.Float(1), hff.Int(1)}, [2]hff.Value{hff.Float(-1), hff.Float(1.5)}, [2]hff.Value{tc.collection, hff.Float(0)}, [2]hff.Value{tc.collection, hff.Float(0.5)})
				}
				if tc.container == "map" {
					key := strings.Trim(taggedCollectionJSON(t, tc.first), `"`)
					pairs = append(pairs, [2]hff.Value{hff.Object(map[string]hff.Value{key: hff.Null()}), tc.first})
				}
				var script strings.Builder
				fmt.Fprintf(&script, "CREATE TEMP TABLE records(id integer,value %s,other %s,items %s,serialized text,items_json text,value_meta jsonb,other_meta jsonb,items_meta jsonb);\n", format, format, format)
				records := make([]hff.Value, len(pairs))
				for i, pair := range pairs {
					xs := hff.List(pair[0], pair[1], pair[0])
					if pair[0].Kind() == hff.InvalidKind || pair[1].Kind() == hff.InvalidKind {
						xs = hff.Null()
					}
					// Separate ordinary empty/null receiver cases from missing paths.
					if i == 11 {
						xs = hff.List()
					}
					if i == 12 {
						xs = hff.Null()
					}
					fields := map[string]hff.Value{"items": xs, "serialized": hff.String("null"), "items_json": hff.String(taggedCollectionJSON(t, xs))}
					values := [2]string{"NULL", "NULL"}
					metas := [2]string{`'{"version":1,"types":{}}'`, `'{"version":1,"types":{}}'`}
					for j, name := range []string{"value", "other"} {
						if pair[j].Kind() == hff.InvalidKind {
							continue
						}
						fields[name] = pair[j]
						values[j] = sqlFixtureString(taggedCollectionJSON(t, pair[j]))
						metas[j] = sqlFixtureString(pairTypeMetadata(t, pair[j]))
					}
					if pair[0].Kind() != hff.InvalidKind {
						fields["serialized"] = hff.String(taggedCollectionJSON(t, pair[0]))
					}
					records[i] = hff.Object(fields)
					fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%s,%s,%s,%s,%s,%s,%s,%s);\n", i, values[0], values[1], sqlFixtureString(taggedCollectionJSON(t, xs)), valuesText(t, fields["serialized"]), valuesText(t, fields["items_json"]), metas[0], metas[1], sqlFixtureString(pairTypeMetadata(t, xs)))
				}
				var bindings []hffsql.Binding
				for _, name := range []string{"value", "other", "items"} {
					var options []hffsql.JSONBindingOption
					if format == hffsql.JSONB && tc.float {
						options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", name+"_meta")))
					}
					bindings = append(bindings, hffsql.BindJSON(hff.Path{name}, hffsql.Column("records", name), format, options...))
				}
				for _, name := range []string{"serialized", "items_json"} {
					bindings = append(bindings, hffsql.Bind(hff.Path{name}, hffsql.Column("records", name)))
				}
				var expected []bool
				var labels []string
				for index, source := range taggedCollectionSources(tc) {
					p, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, p)
					if err != nil {
						t.Fatal(source, err)
					}
					q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
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
				t.Logf("%d query/record results", len(expected))
				runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
			})
		}
	}
}

func valuesText(t testing.TB, v hff.Value) string {
	t.Helper()
	s, ok := v.AsString()
	if !ok {
		t.Fatal("fixture needs string")
	}
	return sqlFixtureString(s)
}

// These schemas already built before container unions were added. Matching
// physical string spellings must not give a tagged scalar String semantics.
func TestPostgreSQLTaggedStringsAreNotStringArguments(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, tc := range taggedCollectionCases() {
		if tc.container != "list" || tc.float || strings.Contains(tc.name, "nestedtrue") {
			continue
		}
		tag := schema.BytesType
		if tc.tag == "duration" {
			tag = schema.DurationType
		}
		if tc.tag == "timestamp" {
			tag = schema.TimestampType
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("data", schema.IntType), schema.Field("needle", schema.Union(tag, schema.IntType, schema.NullType)), schema.Nullable(schema.String("missing")))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		text := strings.Trim(taggedCollectionJSON(t, tc.first), `"`)
		sources := []string{`("` + text + `":contains(needle)):type="null"`, `("` + text + `":contains(needle,missing!)):type="null"`, `data:get(needle):type="null"`, `data:has(needle):type="null"`, `(data:has(needle))!:exists`}
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			filter, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
					hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format),
					hffsql.BindJSON(hff.Path{"needle"}, hffsql.Column("records", "needle"), format),
					hffsql.Bind(hff.Path{"missing"}, hffsql.Column("records", "missing"))))
				if err != nil {
					t.Fatal(source, err)
				}
				for _, needle := range []hff.Value{tc.first, hff.Int(1), hff.Null()} {
					data := hff.Object(map[string]hff.Value{text: hff.Int(1)})
					record := hff.Object(map[string]hff.Value{"data": data, "needle": needle, "missing": hff.Null()})
					want, err := filter.Match(ctx, record)
					if err != nil {
						t.Fatal(err)
					}
					from := fmt.Sprintf("(VALUES (%s::%s,%s::%s,NULL::text)) records(data,needle,missing)", sqlFixtureString(taggedCollectionJSON(t, data)), format, sqlFixtureString(taggedCollectionJSON(t, needle)), format)
					appendAggregateProfileQuery(t, &script, q, from, len(expected))
					expected = append(expected, want)
					labels = append(labels, tc.tag+"/"+string(format)+"/"+source+"/"+needle.Kind().String())
				}
			}
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLJSONObjectDynamicKeysWithoutMetadata(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("data", schema.IntType), schema.Field("key", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	data := hff.Object(map[string]hff.Value{"a": hff.Int(1), "1": hff.Int(2)})
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, source := range []string{`data[key]=1`, `data[key]:type="null"`, `data[key!]:exists`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), hffsql.JSONB),
			hffsql.BindJSON(hff.Path{"key"}, hffsql.Column("records", "key"), hffsql.JSONB)))
		if err != nil {
			t.Fatal(source, err)
		}
		for _, key := range []hff.Value{hff.String("a"), hff.String("absent"), hff.Int(1), hff.Float(1), hff.Float(0.5), hff.Null(), hff.List(), hff.Object(nil)} {
			want, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"data": data, "key": key}))
			if err != nil {
				t.Fatal(err)
			}
			from := fmt.Sprintf("(VALUES (%s::jsonb,%s::jsonb)) records(data,key)", sqlFixtureString(metadataJSONFixture(t, data)), sqlFixtureString(metadataJSONFixture(t, key)))
			appendAggregateProfileQuery(t, &script, q, from, len(expected))
			expected = append(expected, want)
			labels = append(labels, source+"/"+key.Kind().String())
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
