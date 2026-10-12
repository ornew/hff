//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/local"
	hffsql "github.com/ornew/hff/backends/sql"
)

func TestPostgreSQLFlattenedStructureParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	record := func(name, age, child, scores, rate, labels hff.Value, childPresent bool) hff.Value {
		fields := map[string]hff.Value{"name": name, "age": age, "scores": scores, "rate": rate, "labels": labels}
		if childPresent {
			fields["child"] = child
		}
		return hff.Object(map[string]hff.Value{"profile": hff.Object(fields)})
	}
	child := func(name hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"name": name}) }
	rows := []struct {
		value hff.Value
		sql   string
	}{
		{hff.Object(map[string]hff.Value{}), "false,false,'hidden',100,true,false,'hidden',ARRAY[20]::bigint[],1.0,'{}'::json"},
		{hff.Object(map[string]hff.Value{"profile": hff.Null()}), "true,true,'hidden',100,true,false,'hidden',ARRAY[20]::bigint[],1.0,'{}'::json"},
		{record(hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null(), false), "true,false,NULL,NULL,false,false,'hidden',NULL,NULL,NULL"},
		{record(hff.String("Alice"), hff.Int(18), hff.Null(), hff.List(hff.Int(10), hff.Int(20), hff.Null()), hff.Float(math.Copysign(0, -1)), hff.Object(map[string]hff.Value{"app": hff.String("auth")}), true), "true,false,'Alice',18,true,true,'hidden',ARRAY[10,20,NULL]::bigint[],'-0.0'::double precision,'{\"app\":\"auth\"}'::json"},
		{record(hff.String(""), hff.Int(0), child(hff.Null()), hff.List(), hff.Float(0), hff.Object(map[string]hff.Value{}), true), "true,false,'',0,true,false,NULL,ARRAY[]::bigint[],0.0,'{}'::json"},
		{record(hff.String("Bob"), hff.Int(math.MaxInt64), child(hff.String("Father")), hff.List(hff.Int(10), hff.Int(20)), hff.Float(math.SmallestNonzeroFloat64), hff.Null(), true), "true,false,'Bob','9223372036854775807'::bigint,true,false,'Father','[4:5]={10,20}'::bigint[],'5e-324'::double precision,NULL"},
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_flattened(id integer,profile_present boolean,profile_null boolean,profile_name text,profile_age bigint,child_present boolean,child_null boolean,child_name text,scores bigint[],rate double precision,labels json);\n")
	for i, row := range rows {
		fmt.Fprintf(&script, "INSERT INTO hff_flattened VALUES(%d,%s);\n", i, row.sql)
	}
	e := flattenedEngine(t)
	var expected, descriptions []string
	for _, source := range flattenedQueries() {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(flattenedBinding()))
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
			name := fmt.Sprintf("flattened_%d", len(expected))
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
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM hff_flattened AS records WHERE records.id=%d; EXECUTE %s%s; DEALLOCATE %s;\n", name, parameters, query.Where(), i, name, execution, name)
		}
	}
	script.WriteString("DROP TABLE hff_flattened;\n")
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("flattened conformance failed: %v\n%s", err, output)
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
	t.Logf("verified %d flattened query/record pairs", len(expected))
}
