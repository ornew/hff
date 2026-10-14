//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

func TestPostgreSQLDirectRelationAverageMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, family := range []struct {
		typ     hff.Type
		sqlType string
		value   func(int64) hff.Value
	}{
		{schema.IntType, "bigint", hff.Int},
		{schema.FloatType, "double precision", func(v int64) hff.Value { return hff.Float(float64(v)) }},
	} {
		t.Run(family.sqlType, func(t *testing.T) {
			typ := schema.NullableType(family.typ)
			e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.List("items", typ))), schema.Int("threshold"))), engine.WithCapabilities(capabilities.Standard()))
			if err != nil {
				t.Fatal(err)
			}
			relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("hff_average_items"), "item"),
				hffsql.WithCorrelation(hffsql.Equal(hffsql.ColumnValue(hffsql.Column("item", "parent_id"), schema.IntType), hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType))),
				hffsql.WithRelationItem(typ, hffsql.Bind(nil, hffsql.Column("item", "value"))),
				hffsql.WithRelationState(hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "present"), schema.BoolType)), hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "is_null"), schema.BoolType))))
			if err != nil {
				t.Fatal(err)
			}
			rows := []map[string]hff.Value{
				{"items": hff.List(family.value(1), family.value(2), hff.Null()), "threshold": hff.Int(10)},
				{"items": hff.List(), "threshold": hff.Int(20)},
				{"items": hff.List(hff.Null(), hff.Null()), "threshold": hff.Int(30)},
				{"items": hff.Null(), "threshold": hff.Int(40)},
				{"threshold": hff.Int(50)},
			}
			var script strings.Builder
			fmt.Fprintf(&script, "CREATE TEMP TABLE hff_average_roots(id bigint,present boolean,is_null boolean,threshold bigint); CREATE TEMP TABLE hff_average_items(parent_id bigint,value %s);\n", family.sqlType)
			for id, row := range rows {
				value, present := row["items"]
				threshold, _ := row["threshold"].AsInt()
				fmt.Fprintf(&script, "INSERT INTO hff_average_roots VALUES(%d,%t,%t,%d);\n", id, present, value.Kind() == hff.NullKind, threshold)
				values, _ := value.AsList()
				for _, item := range values {
					fmt.Fprintf(&script, "INSERT INTO hff_average_items VALUES(%d,%s);\n", id, sqlFixtureValue(t, item, family.sqlType))
				}
			}
			// Absent/null receivers must ignore physically stored related rows.
			script.WriteString("INSERT INTO hff_average_items VALUES(3,99),(4,99);\n")
			var expected []bool
			var labels []string
			for _, source := range []string{`items:avg=1.5`, `items:avg=null`, `items:avg(x=>x!)=1.5`, `items:avg(x=>x+1)=2.5`, `items:avg(x=>threshold)=30`, `items:avg(x=>null)=null`, `items:avg(x=>x/0)=null`, `items!:avg:exists`} {
				p, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(err)
				}
				filter, err := local.Build(ctx, p)
				if err != nil {
					t.Fatal(err)
				}
				q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation), hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("records", "threshold"))))
				if err != nil {
					t.Fatal(source, err)
				}
				if !strings.Contains(source, "items!") && (strings.Contains(q.Where(), "json_agg") || strings.Contains(q.Where(), "array_elements")) {
					t.Fatal("direct average materialized its relation", q.Where())
				}
				var types, args []string
				for _, arg := range q.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types = append(types, typ)
					args = append(args, literal)
				}
				params, arguments := "", ""
				if len(types) > 0 {
					params = "(" + strings.Join(types, ",") + ")"
					arguments = "(" + strings.Join(args, ",") + ")"
				}
				for id, row := range rows {
					got, err := filter.Match(ctx, hff.Object(row))
					if err != nil {
						t.Fatal(err)
					}
					name := fmt.Sprintf("average_%d", len(expected))
					fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM hff_average_roots AS records WHERE records.id=%d; EXECUTE %s%s; DEALLOCATE %s;\n", name, params, q.Where(), id, name, arguments, name)
					expected = append(expected, got)
					labels = append(labels, fmt.Sprintf("%s row%d", source, id))
				}
			}
			runAverageScript(t, ctx, container, script.String(), expected, labels)
		})
	}
}

func TestPostgreSQLNestedRelationAverageKeepsCaptureScope(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	parent := schema.Struct(schema.Int("price"), schema.List("children", schema.IntType))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("parents", parent))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	children, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("hff_average_children"), "child"),
		hffsql.WithCorrelation(hffsql.Equal(hffsql.ColumnValue(hffsql.Column("child", "parent_id"), schema.IntType), hffsql.ColumnValue(hffsql.Column("parent", "id"), schema.IntType))),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(nil, hffsql.Column("child", "value"))))
	if err != nil {
		t.Fatal(err)
	}
	parents, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("hff_average_parents"), "parent"), hffsql.WithCorrelation(hffsql.Boolean(true)),
		hffsql.WithRelationItem(parent, hffsql.BindStruct(nil, hffsql.Boolean(true), hffsql.Boolean(false), hffsql.Bind(hff.Path{"price"}, hffsql.Column("parent", "price")), hffsql.BindRelation(hff.Path{"children"}, children))))
	if err != nil {
		t.Fatal(err)
	}
	row := hff.Object(map[string]hff.Value{"parents": hff.List(
		hff.Object(map[string]hff.Value{"price": hff.Int(10), "children": hff.List(hff.Int(1))}),
		hff.Object(map[string]hff.Value{"price": hff.Int(20), "children": hff.List(hff.Int(1), hff.Int(2), hff.Int(3))}),
		hff.Object(map[string]hff.Value{"price": hff.Int(100), "children": hff.List()}),
	)})
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_average_parents(id bigint,price bigint); INSERT INTO hff_average_parents VALUES(1,10),(2,20),(3,100); CREATE TEMP TABLE hff_average_children(parent_id bigint,value bigint); INSERT INTO hff_average_children VALUES(1,1),(2,1),(2,2),(2,3);\n")
	var expected []bool
	var labels []string
	for index, source := range []string{`parents:avg(p=>p.children:avg(c=>p.price))=15.0`, `parents:avg(p=>p.children:avg(c=>c+p.price))=16.5`, `parents:avg(p=>p.children:avg)=1.5`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		got, err := filter.Match(ctx, row)
		if err != nil || !got {
			t.Fatalf("bad local fixture %s: %v %v", source, got, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"parents"}, parents)))
		if err != nil {
			t.Fatal(err)
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		fmt.Fprintf(&script, "PREPARE nested%d(%s) AS SELECT %s; EXECUTE nested%d(%s); DEALLOCATE nested%d;\n", index, strings.Join(types, ","), q.Where(), index, strings.Join(args, ","), index)
		expected = append(expected, got)
		labels = append(labels, source)
	}
	runAverageScript(t, ctx, container, script.String(), expected, labels)
}

func TestPostgreSQLRelationAverageKeepsAncestorState(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	group := schema.Struct(schema.List("items", schema.IntType))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("group", group))))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("hff_average_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "parent_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(schema.IntType, hffsql.Bind(nil, hffsql.Column("item", "value"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	binding := hffsql.BindStruct(hff.Path{"group"},
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "present"), schema.BoolType)),
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "is_null"), schema.BoolType)),
		hffsql.BindRelation(hff.Path{"items"}, relation),
	)
	rows := []hff.Value{
		hff.Object(map[string]hff.Value{"group": hff.Object(map[string]hff.Value{"items": hff.List(hff.Int(1), hff.Int(2))})}),
		hff.Object(map[string]hff.Value{"group": hff.Null()}),
		hff.Object(map[string]hff.Value{}),
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_average_roots(id bigint,present boolean,is_null boolean); INSERT INTO hff_average_roots VALUES(0,true,false),(1,true,true),(2,false,false); CREATE TEMP TABLE hff_average_items(parent_id bigint,value bigint); INSERT INTO hff_average_items VALUES(0,1),(0,2),(1,99),(2,99);\n")
	var expected []bool
	var labels []string
	for _, source := range []string{`group.items:avg=1.5`, `group.items:avg=null`, `group.items:avg(x=>x+1)=2.5`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if err != nil {
			t.Fatal(err)
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		for id, row := range rows {
			got, err := filter.Match(ctx, row)
			if err != nil {
				t.Fatal(err)
			}
			name := fmt.Sprintf("ancestor%d", len(expected))
			params, arguments := "", ""
			if len(types) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM hff_average_roots AS records WHERE records.id=%d; EXECUTE %s%s; DEALLOCATE %s;\n", name, params, q.Where(), id, name, arguments, name)
			expected = append(expected, got)
			labels = append(labels, fmt.Sprintf("%s row%d", source, id))
		}
	}
	runAverageScript(t, ctx, container, script.String(), expected, labels)
}

func runAverageScript(t *testing.T, ctx context.Context, container, script string, expected []bool, labels []string) {
	t.Helper()
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("relation average failed: %v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want%d: %s", len(results), len(expected), output)
	}
	for index, result := range results {
		if (result == "t") != expected[index] {
			t.Errorf("%s: SQL=%s Local=%v", labels[index], result, expected[index])
		}
	}
}
