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

func TestPostgreSQLFlattenedRequiredLeafParentParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	rows := []struct {
		value hff.Value
		sql   string
	}{
		{hff.Object(map[string]hff.Value{}), "false,false,100"},
		{hff.Object(map[string]hff.Value{"profile": hff.Null()}), "true,true,100"},
		{hff.Object(map[string]hff.Value{"profile": hff.Object(map[string]hff.Value{"age": hff.Int(0)})}), "true,false,0"},
		{hff.Object(map[string]hff.Value{"profile": hff.Object(map[string]hff.Value{"age": hff.Int(10)})}), "true,false,10"},
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_flattened(id integer,profile_present boolean,profile_null boolean,profile_age bigint);\n")
	for i, row := range rows {
		fmt.Fprintf(&script, "INSERT INTO hff_flattened VALUES(%d,%s);\n", i, row.sql)
	}
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("profile", schema.Struct(schema.Int("age"))))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var expected, descriptions []string
	for _, source := range []string{`profile.age`, `profile.age=null`, `profile.age!=null`, `profile.age>5`, `profile!.age=null`, `!profile!.age=null`, `profile!.age>5`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindStruct(hff.Path{"profile"}, hffsql.TrustedSQL("records.profile_present"), hffsql.TrustedSQL("records.profile_null"), hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "profile_age")))))
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
