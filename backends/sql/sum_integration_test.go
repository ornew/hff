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

func TestPostgreSQLIntegerSumMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", schema.NullableType(schema.IntType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []hff.Value{
		hff.Null(), hff.List(), hff.List(hff.Null()),
		hff.List(hff.Int(1), hff.Null(), hff.Int(2)),
		hff.List(hff.Int(math.MaxInt64), hff.Int(1)),
		hff.List(hff.Int(math.MaxInt64), hff.Int(1), hff.Int(-1)),
	}
	sources := []string{
		`items:sum=0`,
		`items:sum=3`,
		`items:sum=null`,
		`items:sum(v=>v!)=3`,
		`items:sum(v=>null)=0`,
		`items:sum(v=>v+1)=5`,
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			var script strings.Builder
			var expected, labels []string
			for _, source := range sources {
				program, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, program)
				if err != nil {
					t.Fatal(source, err)
				}
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
					hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format)))
				if err != nil {
					t.Fatal(source, err)
				}
				if !strings.Contains(query.Where(), "sum(") {
					t.Fatalf("%s did not lower to SQL SUM: %s", source, query.Where())
				}
				var types, literals []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, literals = append(types, typ), append(literals, literal)
				}
				params, args := "", ""
				if len(types) > 0 {
					params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
				}
				for _, value := range rows {
					matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": value}))
					if err != nil {
						t.Fatal(source, err)
					}
					want := "f"
					if matched {
						want = "t"
					}
					expected = append(expected, want)
					labels = append(labels, source+" on "+metadataJSONFixture(t, value))
					name := fmt.Sprintf("sum_%d", len(expected))
					fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s)) records(items); EXECUTE %s%s; DEALLOCATE %s;\n",
						name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, value)), format, name, args, name)
				}
			}
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script.String())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("PostgreSQL integer sum failed: %v\n%s", err, output)
			}
			actual := strings.Fields(string(output))
			if len(actual) != len(expected) {
				t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
			}
			for i, want := range expected {
				if actual[i] != want {
					t.Errorf("%s: PostgreSQL=%s local=%s", labels[i], actual[i], want)
				}
			}
		})
	}
}

func TestPostgreSQLFloatSumMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.List("items", schema.NullableType(schema.FloatType))))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []hff.Value{
		hff.Null(), hff.List(), hff.List(hff.Null()),
		hff.List(hff.Float(1.5), hff.Null(), hff.Float(2.25)),
		hff.List(hff.Float(math.Copysign(0, -1))),
		hff.List(hff.Float(math.Nextafter(1, 2))),
		hff.List(hff.Float(math.MaxFloat64)),
		hff.List(hff.Float(math.MaxFloat64), hff.Float(math.MaxFloat64)),
	}
	sources := []string{
		`items:sum:type="int"`,
		`items:sum:type="float"`,
		`items:sum=3.75`,
		`items:sum:json="3.75"`,
		`items:sum:string="3.75"`,
		`items:sum:json="1.0000000000000002"`,
		`items:sum:json="0.0"`,
		`items:sum:json="0"`,
		`items:sum=null`,
		`items:sum(v=>v+1.0)=5.75`,
		`items:sum(v=>v+1.0):json="5.75"`,
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			var script strings.Builder
			var expected, labels []string
			for _, source := range sources {
				program, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, program)
				if err != nil {
					t.Fatal(source, err)
				}
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
					hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format)))
				if err != nil {
					t.Fatal(source, err)
				}
				if !strings.Contains(query.Where(), "sum(") || !strings.Contains(query.Where(), "pg_input_is_valid(") {
					t.Fatalf("%s did not lower to validated SQL SUM: %s", source, query.Where())
				}
				var types, literals []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, literals = append(types, typ), append(literals, literal)
				}
				params, args := "", ""
				if len(types) > 0 {
					params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
				}
				for _, value := range rows {
					matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": value}))
					if err != nil {
						t.Fatal(source, err)
					}
					want := "f"
					if matched {
						want = "t"
					}
					expected = append(expected, want)
					labels = append(labels, source+" on "+metadataJSONFixture(t, value))
					name := fmt.Sprintf("float_sum_%d", len(expected))
					fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s)) records(items); EXECUTE %s%s; DEALLOCATE %s;\n",
						name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, value)), format, name, args, name)
				}
			}
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script.String())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("PostgreSQL Float sum failed: %v\n%s", err, output)
			}
			actual := strings.Fields(string(output))
			if len(actual) != len(expected) {
				t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
			}
			for i, want := range expected {
				if actual[i] != want {
					t.Errorf("%s: PostgreSQL=%s local=%s", labels[i], actual[i], want)
				}
			}
		})
	}
}

func TestPostgreSQLFloatSumUsesNumericIntermediate(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.FloatType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:sum:exists`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
	if err != nil {
		t.Fatal(err)
	}
	if len(query.Args()) != 0 {
		t.Fatalf("unexpected arguments: %v", query.Args())
	}
	values := hff.List(hff.Float(math.MaxFloat64), hff.Float(math.MaxFloat64), hff.Float(-math.MaxFloat64))
	filter, err := local.Build(ctx, program)
	if err != nil {
		t.Fatal(err)
	}
	localMatch, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": values}))
	if err != nil {
		t.Fatal(err)
	}
	if localMatch {
		t.Fatal("local list-order Float addition unexpectedly retained a finite intermediate")
	}
	script := fmt.Sprintf("SELECT %s FROM (VALUES (%s::json)) records(items);", query.Where(), sqlFixtureString(metadataJSONFixture(t, values)))
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL numeric intermediate failed: %v\n%s", err, output)
	}
	if got := strings.TrimSpace(string(output)); got != "t" {
		t.Fatalf("PostgreSQL numeric intermediate = %q, want true", got)
	}
}

func TestPostgreSQLMixedSumMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	for _, itemType := range []hff.Type{schema.Union(schema.IntType, schema.FloatType, schema.NullType), schema.AnyType} {
		e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", itemType))), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		rows := []hff.Value{
			hff.List(), hff.List(hff.Null()), hff.List(hff.Int(1), hff.Int(2)),
			hff.List(hff.Int(1), hff.Float(2.5)),
			hff.List(hff.Float(1)), hff.List(hff.Float(math.Copysign(0, -1))),
			hff.List(hff.Int(1), hff.Float(2)),
			hff.List(hff.Float(1.5), hff.Null(), hff.Int(2)),
			hff.List(hff.Int(math.MaxInt64), hff.Int(1)),
			hff.List(hff.Int(math.MaxInt64), hff.Int(1), hff.Int(-1)),
		}
		if itemType.Kind() == hff.AnyType {
			rows = append(rows, hff.List(hff.Int(1), hff.String("invalid")), hff.List(hff.Int(1), hff.Bool(true)))
		}
		for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
			t.Run(itemType.Key()+"/"+string(format), func(t *testing.T) {
				var options []hffsql.JSONBindingOption
				if format == hffsql.JSONB {
					options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
				}
				var script strings.Builder
				var expected, labels []string
				for _, source := range []string{`items:sum=0`, `items:sum=3`, `items:sum=3.5`, `items:sum=null`, `items:sum:[float]:exists`, `items:sum:json="1.0"`, `items:sum:json="3.5"`, `items:sum(x=>x)=0`, `items:sum(x=>x)=3.5`, `items:sum(x=>x):json="1.0"`, `items:sum(x=>x)=null`, `items:sum(x=>x!)=3.5`, `items:sum(x=>x):json="9223372036854775807"`} {
					program, err := e.Compile(ctx, source)
					if err != nil {
						t.Fatal(source, err)
					}
					filter, err := local.Build(ctx, program)
					if err != nil {
						t.Fatal(source, err)
					}
					query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
						hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...)))
					if err != nil {
						t.Fatal(source, err)
					}
					var types, literals []string
					for _, arg := range query.Args() {
						typ, literal := sqlFixtureParameter(t, arg)
						types, literals = append(types, typ), append(literals, literal)
					}
					params, args := "", ""
					if len(types) > 0 {
						params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
					}
					for _, values := range rows {
						matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": values}))
						if err != nil {
							t.Fatal(source, err)
						}
						want := "f"
						if matched {
							want = "t"
						}
						expected = append(expected, want)
						labels = append(labels, source+" on "+metadataJSONFixture(t, values))
						name := fmt.Sprintf("mixed_sum_%d", len(expected))
						fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb)) records(items,metadata); EXECUTE %s%s; DEALLOCATE %s;\n",
							name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, values)), format, sqlFixtureString(pairTypeMetadata(t, values)), name, args, name)
					}
				}
				command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
				command.Stdin = strings.NewReader(script.String())
				output, err := command.CombinedOutput()
				if err != nil {
					t.Fatalf("PostgreSQL mixed sum failed: %v\n%s", err, output)
				}
				actual := strings.Fields(string(output))
				if len(actual) != len(expected) {
					t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
				}
				for i, want := range expected {
					if actual[i] != want {
						t.Errorf("%s: PostgreSQL=%s local=%s", labels[i], actual[i], want)
					}
				}
			})
		}
	}
}

func TestPostgreSQLMixedSumSkipsRejectedJSONFloats(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		json, source string
		want         string
	}{
		{`[1,1e-400]`, `items:sum:json="1"`, "t"},
		{`[1,1e-400]`, `items:sum:[float]:exists`, "f"},
		{`[1e-400]`, `items:sum:json="0"`, "t"},
		{`[1e400,1]`, `items:sum:json="1"`, "t"},
		{`[1,2.5,1e-400]`, `items:sum:json="3.5"`, "t"},
		{`[1,2.5,1e-400]`, `items:sum:[float]:exists`, "t"},
		{`[1,1e-400]`, `items:sum(x=>x):json="1"`, "t"},
		{`[1e-400]`, `items:sum(x=>x):json="0"`, "t"},
		{`[1e400,1]`, `items:sum(x=>x):json="1"`, "t"},
	}
	var script strings.Builder
	for i, tc := range tests {
		program, err := e.Compile(ctx, tc.source)
		if err != nil {
			t.Fatal(tc.source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON)))
		if err != nil {
			t.Fatal(tc.source, err)
		}
		var types, literals []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types, literals = append(types, typ), append(literals, literal)
		}
		params, args := "", ""
		if len(types) > 0 {
			params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
		}
		name := fmt.Sprintf("rejected_sum_%d", i)
		fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::json)) records(items); EXECUTE %s%s; DEALLOCATE %s;\n",
			name, params, query.Where(), sqlFixtureString(tc.json), name, args, name)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL rejected Float sum failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(tests) {
		t.Fatalf("got %d results, want %d: %s", len(actual), len(tests), output)
	}
	for i, tc := range tests {
		if actual[i] != tc.want {
			t.Errorf("%s on %s: PostgreSQL=%s want=%s", tc.source, tc.json, actual[i], tc.want)
		}
	}
}

func TestPostgreSQLMixedCallbackSumMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	price := schema.Union(schema.IntType, schema.FloatType, schema.NullType)
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.Struct(schema.Field("price", price))), schema.Int("threshold"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	object := func(value hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"price": value})
	}
	values := []hff.Value{
		hff.List(),
		hff.List(object(hff.Int(1)), object(hff.Float(2.5))),
		hff.List(object(hff.Null()), object(hff.Int(2))),
		hff.List(object(hff.Float(1))),
	}
	for _, format := range []hffsql.JSONFormat{hffsql.JSON, hffsql.JSONB} {
		t.Run(string(format), func(t *testing.T) {
			var options []hffsql.JSONBindingOption
			if format == hffsql.JSONB {
				options = append(options, hffsql.WithTypeMetadata(hffsql.Column("records", "metadata")))
			}
			var script strings.Builder
			var expected, labels []string
			for _, source := range []string{`items:sum(x=>x.price)=3.5`, `items:sum(x=>x.price)=2`, `items:sum(x=>x.price):json="1.0"`, `items:sum(x=>x.price!)=3.5`, `items:sum(x=>x.price+1)=5.5`, `items:sum(x=>x.price+threshold)=5.5`} {
				program, err := e.Compile(ctx, source)
				if err != nil {
					t.Fatal(source, err)
				}
				filter, err := local.Build(ctx, program)
				if err != nil {
					t.Fatal(source, err)
				}
				query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
					hffsql.WithBindings(hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), format, options...), hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("records", "threshold"))))
				if err != nil {
					t.Fatal(source, err)
				}
				var types, literals []string
				for _, arg := range query.Args() {
					typ, literal := sqlFixtureParameter(t, arg)
					types, literals = append(types, typ), append(literals, literal)
				}
				params, args := "", ""
				if len(types) > 0 {
					params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
				}
				for _, record := range values {
					matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": record, "threshold": hff.Int(1)}))
					if err != nil {
						t.Fatal(source, err)
					}
					want := "f"
					if matched {
						want = "t"
					}
					expected = append(expected, want)
					labels = append(labels, source+" on "+metadataJSONFixture(t, record))
					name := fmt.Sprintf("callback_sum_%d", len(expected))
					fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::%s,%s::jsonb,1::bigint)) records(items,metadata,threshold); EXECUTE %s%s; DEALLOCATE %s;\n",
						name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, record)), format, sqlFixtureString(pairTypeMetadata(t, record)), name, args, name)
				}
			}
			command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
			command.Stdin = strings.NewReader(script.String())
			output, err := command.CombinedOutput()
			if err != nil {
				t.Fatalf("PostgreSQL mixed callback sum failed: %v\n%s", err, output)
			}
			actual := strings.Fields(string(output))
			if len(actual) != len(expected) {
				t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
			}
			for i, want := range expected {
				if actual[i] != want {
					t.Errorf("%s: PostgreSQL=%s local=%s", labels[i], actual[i], want)
				}
			}
		})
	}
}

func TestPostgreSQLSumCapturedOuterValue(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.List("items", schema.IntType), schema.Int("threshold"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	program, err := e.Compile(ctx, `items:sum(v=>threshold)=4`)
	if err != nil {
		t.Fatal(err)
	}
	query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.BindJSON(hff.Path{"items"}, hffsql.Column("records", "items"), hffsql.JSON),
		hffsql.Bind(hff.Path{"threshold"}, hffsql.Column("records", "threshold")),
	))
	if err != nil {
		t.Fatal(err)
	}
	filter, err := local.Build(ctx, program)
	if err != nil {
		t.Fatal(err)
	}
	var types, literals []string
	for _, arg := range query.Args() {
		typ, literal := sqlFixtureParameter(t, arg)
		types, literals = append(types, typ), append(literals, literal)
	}
	params, args := "", ""
	if len(types) > 0 {
		params, args = "("+strings.Join(types, ",")+")", "("+strings.Join(literals, ",")+")"
	}
	var script strings.Builder
	var expected []string
	for i, values := range []hff.Value{hff.List(), hff.List(hff.Int(1)), hff.List(hff.Int(1), hff.Int(2))} {
		matched, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"items": values, "threshold": hff.Int(2)}))
		if err != nil {
			t.Fatal(err)
		}
		if matched {
			expected = append(expected, "t")
		} else {
			expected = append(expected, "f")
		}
		name := fmt.Sprintf("sum_capture_%d", i)
		fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM (VALUES (%s::json,2::bigint)) records(items,threshold); EXECUTE %s%s; DEALLOCATE %s;\n",
			name, params, query.Where(), sqlFixtureString(metadataJSONFixture(t, values)), name, args, name)
	}
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("PostgreSQL captured sum failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("row %d PostgreSQL=%s local=%s", i, actual[i], want)
		}
	}
}
