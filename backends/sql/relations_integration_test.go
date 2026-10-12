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

func TestPostgreSQLRelationParity(t *testing.T)       { runRelationParity(t, false, false, false) }
func TestPostgreSQLNestedRelationParity(t *testing.T) { runRelationParity(t, true, false, false) }
func TestPostgreSQLRelationStateParity(t *testing.T)  { runRelationParity(t, true, true, false) }
func TestPostgreSQLUnorderedTransformParity(t *testing.T) {
	t.Run("Present", func(t *testing.T) { runRelationParity(t, true, false, true) })
	t.Run("Nullable", func(t *testing.T) { runRelationParity(t, true, true, true) })
}
func runRelationParity(t *testing.T, nested, state, unordered bool) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	item := func(price int64, active hff.Value, tags ...hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"price": hff.Int(price), "active": active, "tags": hff.List(tags...)})
	}
	rows := []struct{ value hff.Value }{
		{hff.Object(map[string]hff.Value{"items": hff.List()})},
		{hff.Object(map[string]hff.Value{"items": hff.List(item(20, hff.Bool(true), hff.String("auth"), hff.String("z")), item(5, hff.Bool(false), hff.String("dev")))})},
		{hff.Object(map[string]hff.Value{"items": hff.List(item(0, hff.Null()))})},
		{hff.Object(map[string]hff.Value{"items": hff.List(item(20, hff.Bool(true), hff.String("auth")), item(20, hff.Bool(true), hff.String("auth")))})},
	}
	rows = append(rows, struct{ value hff.Value }{hff.Object(map[string]hff.Value{"items": hff.List(item(20, hff.Null(), hff.String("auth")), item(5, hff.Bool(false), hff.String("dev")))})})
	if state {
		rows[0].value = hff.Object(map[string]hff.Value{})
		rows[1].value = hff.Object(map[string]hff.Value{"items": hff.Null()})
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_parents(id integer); INSERT INTO hff_parents VALUES(0),(1),(2),(3),(4); CREATE TEMP TABLE hff_related(id bigint,parent_id integer,price bigint,active boolean,tags json); INSERT INTO hff_related VALUES(10,1,20,true,'[\"auth\",\"z\"]'),(20,1,5,false,'[\"dev\"]'),(30,2,0,NULL,'[]'),(40,3,20,true,'[\"auth\"]'),(50,3,20,true,'[\"auth\"]'),(60,NULL,20,true,'[\"auth\"]'),(70,4,20,NULL,'[\"auth\"]'),(80,4,5,false,'[\"dev\"]'); CREATE TEMP TABLE hff_tags(item_id bigint,position bigint,value text); INSERT INTO hff_tags SELECT related.id,tag.ord,tag.value FROM hff_related AS related CROSS JOIN LATERAL json_array_elements_text(related.tags) WITH ORDINALITY AS tag(value,ord);\n")

	e := relationEngine(t)
	if state {
		var err error
		e, err = engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.List("items", relationItemType()))))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
	}

	var expected, descriptions []string
	queries := append(relationUnorderedQueries(), relationOrderedQueries()...)
	script.WriteString("CREATE TEMP TABLE reversed_related AS SELECT * FROM hff_related ORDER BY id DESC;TRUNCATE hff_related;INSERT INTO hff_related SELECT * FROM reversed_related;CREATE TEMP TABLE reversed_tags AS SELECT * FROM hff_tags ORDER BY item_id DESC,position DESC;TRUNCATE hff_tags;INSERT INTO hff_tags SELECT * FROM reversed_tags;\n")
	if unordered {
		queries = append(relationUnorderedQueries(), relationOrderBlindQueries()...)
	}
	for _, source := range queries {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(relationBindingConfigured(t, !unordered, nested, true, state)))
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for i, row := range rows {
			match, err := filter.Match(ctx, row.value)
			if err != nil {
				t.Fatal(source, i, err)
			}
			want := "f"
			if match {
				want = "t"
			}
			expected = append(expected, want)
			descriptions = append(descriptions, fmt.Sprintf("%s row%d", source, i))
			name := fmt.Sprintf("relation_%d", len(expected))
			var types, literals []string
			for _, arg := range query.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				literals = append(literals, literal)
			}
			parameters, execution := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				execution = "(" + strings.Join(literals, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM hff_parents AS records WHERE records.id=%d; EXECUTE %s%s; DEALLOCATE %s;\n", name, parameters, query.Where(), i, name, execution, name)
		}
	}
	script.WriteString("DROP TABLE hff_tags; DROP TABLE hff_related; DROP TABLE hff_parents;\n")
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("relation conformance failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results; want %d", len(actual), len(expected))
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("%s PostgreSQL=%s local=%s", descriptions[i], actual[i], want)
		}
	}
	t.Logf("verified %d relation query/record pairs", len(expected))
}
