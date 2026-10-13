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
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLNativeFloatModel(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Float("left")), schema.Nullable(schema.Float("right")), schema.String("text"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var labels []string
	for i, tc := range []struct {
		source      string
		left, right float64
		text        string
		digits      int
	}{
		{`(left+right)=null`, math.MaxFloat64, math.MaxFloat64, "", 3},
		{`(left*right)=null`, math.MaxFloat64, 2, "", 3},
		{`(left/right)=null`, 1, 0, "", 3},
		{`(left/right)=null`, 0, math.Copysign(0, -1), "", 3},
		{`(left/right)=null`, math.SmallestNonzeroFloat64, 1e100, "", 3},
		{`(left-left)=0`, math.MaxFloat64, 0, "", 3},
		{`(left+right)=0`, math.MaxFloat64, -math.MaxFloat64, "", 3},
		{`(left-right)>0`, math.MaxFloat64, math.Nextafter(math.MaxFloat64, 0), "", 3},
		{`(left*right):string="-0.0"`, math.Copysign(0, -1), 2, "", 3},
		{`(left/right):string="-0.0"`, 0, -2, "", 3},
		{`(left*right):exists`, 1e100, 1e100, "", 3},
		{`(left*right):exists`, math.Nextafter(1e100, math.Inf(1)), 1e100, "", 3},
		{`(left/right):exists`, 1e-100, 1e100, "", 3},
		{`(left/right)=1`, math.MaxFloat64, math.MaxFloat64, "", 3},
		{`text:number=null`, 0, 0, "1e-400", 3},
		{`text:number=null`, 0, 0, "1e400", 3},
		{`text:fromjson=null`, 0, 0, "1e-400", 3},
		{`text:fromjson=null`, 0, 0, `{"a":[1,-1e-400]}`, 3},
		{`text:number=null`, 0, 0, "NaN", 3},
		{`left:string="1.2345678901234567"`, 1.2345678901234567, 0, "", 3},
		{`left:string="1.2345678901234567"`, 1.2345678901234567, 0, "", 1},
		{`left:round=2`, 2.5, 0, "", 3},
		{`left:round=-2`, -2.5, 0, "", 3},
	} {
		p, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.Bind(hff.Path{"left"}, hffsql.Column("records", "left")),
			hffsql.Bind(hff.Path{"right"}, hffsql.Column("records", "right")),
			hffsql.Bind(hff.Path{"text"}, hffsql.Column("records", "text")),
		))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
		}
		fmt.Fprintf(&script, "SET extra_float_digits=%d; PREPARE native_%d%s AS SELECT %s FROM (VALUES (%s,%s,%s::text)) records(\"left\",\"right\",\"text\"); EXECUTE native_%d%s;\n", tc.digits, i, parameters, q.Where(), sqlFixtureValue(t, hff.Float(tc.left), "double precision"), sqlFixtureValue(t, hff.Float(tc.right), "double precision"), sqlFixtureString(tc.text), i, arguments)
		labels = append(labels, tc.source)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(labels) {
		t.Fatalf("got %d results,want %d: %s", len(results), len(labels), output)
	}
	for i, result := range results {
		if result != "t" {
			t.Errorf("%s: SQL=%s", labels[i], result)
		}
	}
}

func TestPostgreSQLNativeFloatStructuralPrecision(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	left := schema.Union(schema.StringType, schema.ListOf(schema.FloatType))
	right := schema.Union(schema.StringType, schema.ListOf(schema.IntType))
	var script strings.Builder
	for i, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		q := pairDispatchQuery(t, left, right, `left!=right`, format, false)
		var types, args []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, args = append(types, typ), append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters, arguments = "("+strings.Join(types, ",")+")", "("+strings.Join(args, ",")+")"
		}
		for _, digits := range []int{1, 3} {
			// Public Float formatting must not reduce the represented value
			// before exact mixed comparisons in supported session settings.
			fmt.Fprintf(&script, "SET extra_float_digits=%d; PREPARE precise_%d_%d%s AS SELECT %s FROM (VALUES ('[1.0000000000000002]'::%s,'[1]'::%s),('[9007199254740992]'::%s,'[9007199254740993]'::%s)) records(\"left\",\"right\"); EXECUTE precise_%d_%d%s;\n", digits, i, digits, parameters, q.Where(), format, format, format, format, i, digits, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != 8 {
		t.Fatalf("got %d results,want 8: %s", len(results), output)
	}
	for _, result := range results {
		if result != "t" {
			t.Errorf("internal Float transport rounded a distinct value: %s", output)
		}
	}
}
