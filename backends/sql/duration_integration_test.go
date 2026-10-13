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
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestPostgreSQLNativeDurationParity(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Duration("elapsed")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	values := []hff.Value{
		hff.Null(), hff.Duration(0), hff.Duration(time.Nanosecond), hff.Duration(-time.Nanosecond),
		hff.Duration(time.Minute), hff.Duration(time.Duration(math.MaxInt64)), hff.Duration(time.Duration(math.MinInt64)),
	}
	sources := []string{
		`elapsed`, `!elapsed`, `elapsed=null`, `elapsed:exists`, `elapsed:type="duration"`,
		`elapsed:seconds>=60`, `elapsed:nanoseconds=1`, `elapsed>"-1ns":duration`,
		`elapsed:in("1ns":duration,null)`, `elapsed:string="0s"`,
		`elapsed:json="\"-2562047h47m16.854775808s\""`, `elapsed:[duration]:exists`,
	}
	var script strings.Builder
	var labels []string
	var expected []bool
	for sourceIndex, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.Bind(hff.Path{"elapsed"}, hffsql.Column("records", "elapsed"))))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		parameters, arguments := "", ""
		if len(types) != 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		var rows []string
		for rowIndex, value := range values {
			record := hff.Object(map[string]hff.Value{"elapsed": value})
			match, err := filter.Match(ctx, record)
			if err != nil {
				t.Fatal(source, rowIndex, err)
			}
			expected = append(expected, match)
			labels = append(labels, fmt.Sprintf("%s row%d", source, rowIndex))
			rows = append(rows, "("+sqlFixtureValue(t, value, "bigint")+")")
		}
		fmt.Fprintf(&script, "PREPARE native_duration_%d%s AS SELECT %s FROM (VALUES %s) AS records(elapsed); EXECUTE native_duration_%d%s; DEALLOCATE native_duration_%d;\n",
			sourceIndex, parameters, query.Where(), strings.Join(rows, ","), sourceIndex, arguments, sourceIndex)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	results := strings.Fields(string(output))
	if len(results) != len(expected) {
		t.Fatalf("got %d SQL results; want %d: %s", len(results), len(expected), output)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}

func TestPostgreSQLNativeDurationRelationCallbacksMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	itemType := schema.NullableType(schema.DurationType)
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.List("items", itemType), schema.Int("threshold"),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("duration_items"), "item"),
		hffsql.WithCorrelation(hffsql.TrustedSQL("item.parent_id=records.id",
			hffsql.Column("item"), hffsql.Column("records"))),
		hffsql.WithRelationItem(itemType, hffsql.Bind(nil, hffsql.Column("item", "elapsed_ns"))),
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		items     hff.Value
		threshold int64
	}{
		{hff.List(), 1},
		{hff.List(hff.Null()), 1},
		{hff.List(hff.Duration(0)), 0},
		{hff.List(hff.Duration(time.Nanosecond), hff.Null()), 1},
		{hff.List(hff.Duration(time.Duration(math.MinInt64))), math.MinInt64},
	}
	sources := []string{
		`items:any(x=>x:nanoseconds=threshold)`,
		`items:any(x=>x=null)`,
		`items:any(x=>x!:nanoseconds=1)`,
		`items:all(x=>x:exists)`,
		`items:all(x=>x:nanoseconds=threshold)`,
		`items:size=0`,
		`items:size=2`,
		`items:has(-1)`,
		`items:has(1.5)`,
		`items:has(threshold)`,
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE duration_parents(id integer,threshold bigint);" +
		"CREATE TEMP TABLE duration_items(parent_id integer,elapsed_ns bigint);" +
		"INSERT INTO duration_parents VALUES (0,1),(1,1),(2,0),(3,1),(4,'-9223372036854775808'::bigint);" +
		"INSERT INTO duration_items VALUES (1,NULL),(2,0),(3,1),(3,NULL),(4,'-9223372036854775808'::bigint);\n")
	var expected, labels []string
	for si, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindRelation(hff.Path{"items"}, relation),
			hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("records", "threshold")),
		))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, arguments []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			arguments = append(arguments, literal)
		}
		name := fmt.Sprintf("native_duration_relation_%d", si)
		allTypes := append(append([]string(nil), types...), "integer")
		fmt.Fprintf(&script, "PREPARE %s(%s) AS SELECT %s FROM duration_parents AS records WHERE records.id=$%d;\n",
			name, strings.Join(allTypes, ","), query.Where(), len(types)+1)
		for ri, row := range rows {
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
				"items": row.items, "threshold": hff.Int(row.threshold),
			}))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			want := "f"
			if match {
				want = "t"
			}
			expected = append(expected, want)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			args := fmt.Sprintf("%d", ri)
			if len(arguments) > 0 {
				args = strings.Join(arguments, ",") + "," + args
			}
			fmt.Fprintf(&script, "EXECUTE %s(%s);\n", name, args)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("%s: SQL=%s local=%s", labels[i], actual[i], want)
		}
	}
}

func TestPostgreSQLComputedDurationConversions(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	stringsIn := []string{"0", "+0", "-0", "0s", "-0.0ns", "+1h30m", "1h30m.5s", ".5h", "1.h", "1h0m0s", "1s1s", "0.4ns0.6ns", "0.4ns", "0.9ns0.1ns", "0.999ns0.001ns", "1.000000000ns", "1.0000000001ns", "-.5ns.5ns", "-1ns", "1ms", "1us", "1ns", "3600s", "60s", "3601s", "61.000000001s", "9223372036854775807ns", "9223372036854775808ns", "-9223372036854775808ns", "-9223372036854775809ns", "000000000000000000000000000000000000000001ns", "99999999999999999999999999999999999h", "2562047h47m16.854775807s", "-2562047h47m16.854775808s", "", "+", "-", "00", "0.0", "1", "1d", "1w", "1µs", "1μs", "1NS", "1e3ns", " 1s", "1s ", "1s\n", "1s+2s", "1s-2s", ".ns", "١s", "NaNns", `["0.4ns0.6ns","wrong","2ns"]`}
	values := []hff.Value{hff.Null(), hff.Int(1), hff.Bool(true), hff.List(), hff.Object(nil), hff.Float(1)}
	for _, s := range stringsIn {
		values = append(values, hff.String(s))
	}
	sources := []string{`data:fromjson:[list]:map(x=>x:duration:nanoseconds):json="[1,null,2]"`, `data:duration:in("0ns":duration,"1ns":duration,null)`, `data:duration:in((null!):duration):json="null"`, `data:duration:in(null):json="false"`, `data:duration:in(1,"1ns",true,["1ns"],null):json="false"`, `data:duration:in(1,"1ns":duration)`, `data:duration:in(data:fromjson:[string|int]):json="false"`, `data:duration`, `!data:duration`, `data:duration=null`, `data:duration!=null`, `data!:duration:exists`, `data:duration:type="duration"`, `data:duration:type="null"`, `data:duration:[duration]:exists`, `data:duration:[int]:exists`, `data:duration:[duration|null]:type="null"`, `data:duration:nanoseconds=1`, `data:duration:nanoseconds=-1`, `data:duration:nanoseconds=9223372036854775807`, `data:duration:nanoseconds=-9223372036854775808`, `data:duration=("0.4ns0.6ns":duration)`, `data:duration>("0":duration)`, `data:duration<("0":duration)`, `data:duration:hours=(data:duration:nanoseconds/3600000000000)`, `data:duration:minutes=(data:duration:nanoseconds/60000000000)`, `data:duration:seconds=(data:duration:nanoseconds/1000000000)`, `data:duration:milliseconds=(data:duration:nanoseconds/1000000)`, `data:duration:microseconds=(data:duration:nanoseconds/1000)`, `data:duration:string="0s"`, `data:duration:string="0.000000001s"`, `data:duration:json="\"0.000000001s\""`, `data:duration:string="-2562047h47m16.854775808s"`, `data:duration:string="2562047h47m16.854775807s"`, `data:duration:string="null"`, `data:duration:json="null"`, `data:duration:string:duration=data:duration`, `data:duration:[duration|null]:string="null"`}
	var script strings.Builder
	var expected []bool
	var labels []string
	for fi, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB, "", hffsql.JSONB} {
		typ := schema.AnyType
		if format == "" {
			typ = schema.Union(schema.StringType, schema.NullType)
		}
		if fi == 3 {
			typ = schema.Union(schema.StringType, schema.FloatType, schema.NullType)
		}
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.Field("data", typ))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		for si, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(source, err)
			}
			f, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(err)
			}
			binding := hffsql.BindJSON(hff.Path{"data"}, hffsql.Column("records", "data"), format)
			if format == "" {
				binding = hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
			if err != nil {
				t.Fatal(source, err)
			}
			var rows, types, args []string
			for _, arg := range q.Args() {
				typ, lit := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, lit)
			}
			for i, v := range values {
				if format == "" && v.Kind() != hff.StringKind && v.Kind() != hff.NullKind {
					continue
				}
				if fi == 3 {
					if _, err := e.Validate(ctx, hff.Object(map[string]hff.Value{"data": v})); err != nil {
						continue
					}
				}
				want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"data": v}))
				if err != nil {
					t.Fatal(source, i, err)
				}
				row := "(" + sqlFixtureString(metadataJSONFixture(t, v)) + "::" + string(format) + ")"
				if format == "" {
					row = "(" + sqlFixtureValue(t, v, "text") + ")"
				}
				rows = append(rows, row)
				expected = append(expected, want)
				labels = append(labels, fmt.Sprintf("%s %s input%d", source, format, i))
			}
			parameters, arguments := "", ""
			if len(types) > 0 {
				parameters = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			fmt.Fprintf(&script, "PREPARE duration_%d_%d%s AS SELECT %s FROM (VALUES %s) records(data); EXECUTE duration_%d_%d%s;\n", fi, si, parameters, q.Where(), strings.Join(rows, ","), fi, si, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got%d want%d: %s", len(results), len(expected), out)
	}
	for i, r := range results {
		if (r == "t") != expected[i] {
			t.Errorf("%s: SQL=%s local=%v", labels[i], r, expected[i])
		}
	}
	t.Logf("compared %d computed duration results", len(results))
}

func TestPostgreSQLDurationBeyondNumericScale(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.String("data"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	// Each component exceeds numeric's fractional scale, but their exact sum
	// is one nanosecond. Rejecting either component individually is incorrect.
	tiny := "0." + strings.Repeat("0", 19999) + "1ns"
	complement := "0." + strings.Repeat("9", 20000) + "ns"
	inputs := []string{tiny + complement, "-" + tiny + complement, tiny, "1." + strings.Repeat("0", 20000) + "ns"}
	var script strings.Builder
	var wants []bool
	for index, source := range []string{`data:duration:nanoseconds=1 | data:duration:nanoseconds=-1`, `data:duration:string:duration:nanoseconds=1 | data:duration:string:duration:nanoseconds=-1`} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(err)
		}
		f, err := local.Build(ctx, p, local.WithLimits(hff.Limits{MaxSteps: math.MaxInt}))
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"data"}, hffsql.Column("records", "data"))))
		if err != nil {
			t.Fatal(err)
		}
		var types, args, rows []string
		for _, arg := range q.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, literal)
		}
		for _, input := range inputs {
			want, err := f.Match(ctx, hff.Object(map[string]hff.Value{"data": hff.String(input)}))
			if err != nil {
				t.Fatal(err)
			}
			wants = append(wants, want)
			rows = append(rows, "("+sqlFixtureString(input)+")")
		}
		fmt.Fprintf(&script, "PREPARE precise_duration_%d(%s) AS SELECT %s FROM (VALUES %s) records(data); EXECUTE precise_duration_%d(%s);\n", index, strings.Join(types, ","), q.Where(), strings.Join(rows, ","), index, strings.Join(args, ","))
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(wants) {
		t.Fatalf("got %d results, want %d", len(results), len(wants))
	}
	for i, result := range results {
		if (result == "t") != wants[i] {
			t.Errorf("case%d: SQL=%s local=%v", i, result, wants[i])
		}
	}
	t.Logf("compared %d long-decimal duration results", len(results))
}

func TestPostgreSQLDurationPatternEscaping(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	q, err := hffsql.Build(ctx, compile(t, `name:duration:nanoseconds=1000`), hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings()...))
	if err != nil {
		t.Fatal(err)
	}
	if args := q.Args(); len(args) != 1 || args[0] != int64(1000) {
		t.Fatalf("unexpected arguments: %v", args)
	}
	var script strings.Builder
	for i, mode := range []string{"on", "off"} {
		fmt.Fprintf(&script, "SET standard_conforming_strings=%s; PREPARE escape_duration_%d(bigint) AS SELECT %s FROM (VALUES ('1us'),('.5us.5us'),('1e3ns')) records(name); EXECUTE escape_duration_%d(1000);\n", mode, i, q.Where(), i)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if got := strings.Join(strings.Fields(string(out)), " "); got != "t t f t t f" {
		t.Fatalf("duration grammar changed with SQL string escaping: %s", out)
	}
}
