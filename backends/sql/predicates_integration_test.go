//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLStructuredBindingPredicates(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	typ := schema.Struct(schema.Int("age"), schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(schema.String("name"), schema.Optional(schema.String("nickname")))))))
	e, err := engine.New(engine.WithSchema(typ), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		source   string
		present  hffsql.SQLPredicate
		expected string
		args     int
	}{
		{`age>1`, hffsql.Equal(hffsql.ColumnValue(hffsql.Column("records", "tenant"), schema.StringType), hffsql.Literal(hff.String("tenant"))), "t", 1},
		{`profile.nickname=null`, hffsql.Equal(hffsql.ColumnValue(hffsql.Column("records", "tenant"), schema.StringType), hffsql.Literal(hff.String("tenant"))), "t", 0},
		{`profile.name="Alice"`, hffsql.Equal(hffsql.ColumnValue(hffsql.Column("records", "tenant"), schema.StringType), hffsql.Literal(hff.String("tenant"))), "t", 2},
		{`profile.name="Alice"`, hffsql.Equal(hffsql.ColumnValue(hffsql.Column("records", "tenant"), schema.StringType), hffsql.Literal(hff.String("other"))), "f", 2},
		{`profile.name="Alice"`, hffsql.IsNotNull(hffsql.ColumnValue(hffsql.Column("records", "name"), schema.StringType)), "t", 1},
		{`profile.name="Alice"`, hffsql.AsPredicate(hffsql.Literal(hff.Bool(false))), "f", 2},
		{`profile.name="Alice"`, hffsql.And(hffsql.TrustedSQL("TRUE OR FALSE"), hffsql.Boolean(false)), "f", 1},
		{`profile.name="Alice"`, hffsql.Not(hffsql.TrustedSQL("TRUE AND FALSE")), "t", 1},
		{`profile.name="Alice"`, hffsql.Or(hffsql.Boolean(false), hffsql.TrustedSQL("TRUE AND FALSE")), "f", 1},
	}
	var script strings.Builder
	for index, tc := range cases {
		p, err := e.Compile(context.Background(), tc.source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "age")), hffsql.BindStruct(hff.Path{"profile"}, tc.present, hffsql.Boolean(false), hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "name")))))
		if err != nil {
			t.Fatal(err)
		}
		if len(q.Args()) != tc.args {
			t.Fatalf("%s unused args %v", tc.source, q.Args())
		}
		var types, literals []string
		for _, arg := range q.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			literals = append(literals, value)
		}
		params, args := "", ""
		if len(types) > 0 {
			params = "(" + strings.Join(types, ",") + ")"
			args = "(" + strings.Join(literals, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE binding_%d%s AS SELECT %s FROM (VALUES (18::bigint,'Alice'::text,'tenant'::text)) records(age,name,tenant); EXECUTE binding_%d%s;\n", index, params, q.Where(), index, args)
	}
	command := exec.Command("docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(cases) {
		t.Fatalf("results %s", output)
	}
	for index, result := range results {
		if result != cases[index].expected {
			t.Errorf("%s: got%s want%s", cases[index].source, result, cases[index].expected)
		}
	}
}

func TestPostgreSQLTypedRelationCorrelation(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	item := schema.Struct(schema.Int("price"), schema.Nullable(schema.Bool("active")))
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", item))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("hff_typed_items"), "item"), hffsql.WithCorrelation(hffsql.And(
		hffsql.Equal(hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType), hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType)),
		hffsql.Equal(hffsql.ColumnValue(hffsql.Column("orders", "tenant_id"), schema.IntType), hffsql.Literal(hff.Int(17))),
	)), hffsql.WithRelationItem(item, hffsql.BindStruct(nil, hffsql.Boolean(true), hffsql.Boolean(false), hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")), hffsql.Bind(hff.Path{"active"}, hffsql.Column("item", "active")))), hffsql.WithUniqueOrder(hffsql.Ascending(hffsql.Column("item", "id"), schema.IntType)))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_typed_orders(id bigint PRIMARY KEY,tenant_id bigint NOT NULL); INSERT INTO hff_typed_orders VALUES(42,17),(43,17),(44,17),(45,99); CREATE TEMP TABLE hff_typed_items(id bigint PRIMARY KEY,order_id bigint,price bigint,active boolean); INSERT INTO hff_typed_items VALUES(7,42,125,true),(8,42,35,false),(9,43,200,NULL),(10,45,125,true);\n")
	sources := []string{`items:any(item=>item.price>100 item.active)`, `items!:any(item=>item.price>100 item.active)`, `items[0].price=125`, `items:all(item=>item.active)`, `items:any(item=>item.active)`, `items:all(item=>item.price>100 | item.active)`}
	for index, source := range sources {
		p, err := e.Compile(context.Background(), source)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindRelation(hff.Path{"items"}, relation)))
		if err != nil {
			t.Fatal(source, err)
		}
		if index != 2 && strings.Contains(q.Where(), `FROM "hff_typed_items" AS "item" CROSS JOIN LATERAL`) {
			t.Fatalf("%s materialized a native scalar callback: %s", source, q.Where())
		}
		var types, literals []string
		for _, arg := range q.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			literals = append(literals, value)
		}
		fmt.Fprintf(&script, "PREPARE typed_relation_%d(%s) AS SELECT orders.id FROM hff_typed_orders AS orders WHERE %s ORDER BY orders.id; EXECUTE typed_relation_%d(%s);\n", index, strings.Join(types, ","), q.Where(), index, strings.Join(literals, ","))
	}
	command := exec.Command("docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	if strings.Join(strings.Fields(string(output)), ",") != "42,42,42,44,45,42,43,44,45" {
		t.Fatalf("unexpected matching ids: %s", output)
	}
}
