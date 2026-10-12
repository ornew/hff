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

func TestPostgreSQLRelationAliasCaptureParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	binding := func(path, table string) hffsql.Binding {
		relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column(table), "item"), hffsql.WithCorrelation(hffsql.TrustedSQL("item.parent_id=records.id", hffsql.Column("item"), hffsql.Column("records"))), hffsql.WithRelationItem(schema.IntType, hffsql.Bind(hff.Path{}, hffsql.Column("item", "value"))))
		if err != nil {
			t.Fatal(err)
		}
		return hffsql.BindRelation(hff.Path{path}, relation)
	}
	record := func(numbers, others []hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"numbers": hff.List(numbers...), "others": hff.List(others...)})
	}
	rows := []struct{ value hff.Value }{
		{record([]hff.Value{hff.Int(1), hff.Int(2)}, []hff.Value{hff.Int(3)})},
		{record([]hff.Value{hff.Int(3)}, []hff.Value{hff.Int(3)})},
		{record(nil, nil)},
		{record([]hff.Value{hff.Int(2)}, []hff.Value{hff.Int(1)})},
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_parents(id integer);INSERT INTO hff_parents VALUES(0),(1),(2),(3);CREATE TEMP TABLE hff_numbers(parent_id integer,value bigint);CREATE TEMP TABLE hff_others(parent_id integer,value bigint);INSERT INTO hff_numbers VALUES(0,1),(0,2),(1,3),(3,2);INSERT INTO hff_others VALUES(0,3),(1,3),(3,1);\n")
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("numbers", schema.IntType), schema.List("others", schema.IntType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}

	var expected, descriptions []string
	for _, source := range []string{`numbers:any(x=>others:any(y=>x=y))`, `numbers:all(x=>others:all(y=>x<y))`, `numbers:any(x=>others:any(y=>y>x))`, `others:any(x=>numbers:all(x=>x>0))`, `numbers:any(x=>others:any(y=>y=x+1))`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding("numbers", "hff_numbers"), binding("others", "hff_others")))
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
	script.WriteString("DROP TABLE hff_numbers;DROP TABLE hff_others;DROP TABLE hff_parents;\n")
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
