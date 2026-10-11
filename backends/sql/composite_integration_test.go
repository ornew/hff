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

func TestPostgreSQLCompositeParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	profile := func(name, age, father, scores, factors, service, people hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"name": name, "age": age, "father": father, "scores": scores, "factors": factors, "service.name": service, "people": people})
	}
	child := func(name hff.Value) hff.Value { return hff.Object(map[string]hff.Value{"name": name}) }
	rows := []struct {
		value hff.Value
		sql   string
	}{
		{hff.Null(), "NULL::hff_profile"},
		{profile(hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null(), hff.Null()), "ROW(NULL,NULL,NULL,NULL,NULL,NULL,NULL)::hff_profile"},
		{profile(hff.String("Alice"), hff.Int(18), hff.Null(), hff.List(hff.Int(10), hff.Int(20), hff.Null()), hff.List(hff.Float(math.Copysign(0, -1)), hff.Float(1)), hff.String("auth"), hff.List(child(hff.String("A")), hff.Null(), child(hff.Null()))), "ROW('Alice',18,NULL,ARRAY[10,20,NULL]::bigint[],ARRAY['-0.0'::double precision,1.0],'auth',ARRAY[ROW('A')::hff_child,NULL::hff_child,ROW(NULL)::hff_child])::hff_profile"},
		{profile(hff.Null(), hff.Int(0), child(hff.Null()), hff.List(), hff.List(), hff.Null(), hff.List()), "ROW(NULL,0,ROW(NULL)::hff_child,ARRAY[]::bigint[],ARRAY[]::double precision[],NULL,ARRAY[]::hff_child[])::hff_profile"},
		{profile(hff.String("Bob"), hff.Int(math.MaxInt64), child(hff.String("Father")), hff.List(hff.Int(math.MaxInt64)), hff.List(hff.Float(math.SmallestNonzeroFloat64), hff.Float(math.MaxFloat64)), hff.String("a\n日本語"), hff.List(child(hff.Null()), child(hff.Null()))), "ROW('Bob','9223372036854775807'::bigint,ROW('Father')::hff_child,ARRAY['9223372036854775807'::bigint],ARRAY['5e-324'::double precision,'1.7976931348623157e308'::double precision]," + sqlFixtureString("a\n日本語") + ",ARRAY[ROW(NULL)::hff_child,ROW(NULL)::hff_child])::hff_profile"},
		{profile(hff.String(""), hff.Int(math.MinInt64), child(hff.Null()), hff.List(hff.Int(10), hff.Int(20)), hff.List(hff.Float(0)), hff.Null(), hff.Null()), "ROW('','-9223372036854775808'::bigint,ROW(NULL)::hff_child,'[4:5]={10,20}'::bigint[],ARRAY[0.0]::double precision[],NULL,NULL)::hff_profile"},
	}
	var script strings.Builder
	script.WriteString("CREATE TYPE hff_child AS (name text); CREATE TYPE hff_profile AS (name text,age bigint,father hff_child,scores bigint[],factors double precision[],\"service.name\" text,people hff_child[]); CREATE TEMP TABLE hff_composites(id integer,profile hff_profile);\n")
	for i, row := range rows {
		fmt.Fprintf(&script, "INSERT INTO hff_composites VALUES(%d,%s);\n", i, row.sql)
	}
	e := compositeEngine(t)
	var expected, descriptions []string
	for _, source := range compositeQueries() {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindComposite(hff.Path{"profile"}, hffsql.Column("records", "profile"))))
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, p)
		if err != nil {
			t.Fatal(err)
		}
		for i, row := range rows {
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"profile": row.value}))
			if err != nil {
				t.Fatal(source, i, err)
			}
			want := "f"
			if match {
				want = "t"
			}
			expected = append(expected, want)
			descriptions = append(descriptions, fmt.Sprintf("%s row%d", source, i))
			name := fmt.Sprintf("composite_%d", len(expected))
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
			fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM hff_composites AS records WHERE records.id=%d; EXECUTE %s%s; DEALLOCATE %s;\n", name, parameters, query.Where(), i, name, execution, name)
		}
	}
	script.WriteString("DROP TABLE hff_composites; DROP TYPE hff_profile; DROP TYPE hff_child;\n")
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("composite conformance failed: %v\n%s", err, output)
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
	t.Logf("verified %d composite query/record pairs", len(expected))
}
