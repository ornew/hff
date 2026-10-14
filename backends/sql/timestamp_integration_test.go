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

func TestPostgreSQLStoredTimestampsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Timestamp("created")))),
		engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value     hff.Value
		instant   string
		remainder int
	}{
		{hff.Null(), "", 0},
		{hff.Timestamp(time.Date(0, 1, 1, 0, 0, 0, 0, time.UTC)), "0001-01-01 00:00:00+00 BC", 0},
		{hff.Timestamp(time.Date(1969, 12, 31, 23, 59, 59, 999999999, time.UTC)), "1969-12-31 23:59:59.999999+00", 999},
		{hff.Timestamp(time.Date(2026, 10, 1, 0, 0, 0, 123, time.UTC)), "2026-10-01 00:00:00+00", 123},
		{hff.Timestamp(time.Date(2026, 1, 1, 0, 0, 0, 999999999, time.UTC)), "2026-01-01 00:00:00.999999+00", 999},
		{hff.Timestamp(time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC)), "9999-12-31 23:59:59.999999+00", 999},
	}
	sources := []string{
		`created=null`, `created`, `created:type="timestamp"`, `created:exists`,
		`created:[timestamp]:exists`, `created:[int]:exists`,
		`created:[timestamp|null]:type="null"`,
		`created="0000-01-01T00:00:00Z":timestamp`,
		`created!="2026-10-01T00:00:00.000000123Z":timestamp`,
		`created<="1969-12-31T23:59:59.999999999Z":timestamp`,
		`created>"1969-12-31T23:59:59.999999999Z":timestamp`,
		`created>="2026-10-01T00:00:00.000000123Z":timestamp`,
		`!(created="2026-10-01T00:00:00.000000123Z":timestamp)`,
		`created:after("2026-09-30T23:59:59.999999999Z":timestamp)`,
		`created:before("1970-01-01T00:00:00Z":timestamp)`,
		`created:in("0000-01-01T00:00:00Z":timestamp,"2026-10-01T00:00:00.000000123Z":timestamp)`,
		`created:year=0`, `created:month=10`, `created:dayofyear=274`,
		`created:milliseconds=999`,
		`created:string="0000-01-01T00:00:00Z"`,
		`created:string="1969-12-31T23:59:59.999999999Z"`,
		`created:string="2026-01-01T00:00:00.999999999Z"`,
		`created:string="9999-12-31T23:59:59.999999999Z"`,
		`created:json="\"2026-10-01T00:00:00.000000123Z\""`,
		`created:add("1ns":duration)=null`,
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for i, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
			hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("events", "created_at"),
				hffsql.WithNanosecondRemainder(hffsql.Column("events", "created_ns_remainder"))),
		))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, value)
		}
		var values []string
		for ri, row := range rows {
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"created": row.value}))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, match)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			if row.instant == "" {
				values = append(values, "(NULL::timestamptz,NULL::smallint)")
			} else {
				values = append(values, fmt.Sprintf("(%s::timestamptz,%d::smallint)", sqlFixtureString(row.instant), row.remainder))
			}
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE timestamp_%d%s AS SELECT %s FROM (VALUES %s) events(created_at,created_ns_remainder); EXECUTE timestamp_%d%s;\n",
			i, parameters, query.Where(), strings.Join(values, ","), i, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: PostgreSQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}

func TestPostgreSQLPhysicalTimestampComparisonsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(
		schema.Nullable(schema.Timestamp("created")),
		schema.Nullable(schema.Timestamp("updated")),
	)), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	stamp := func(year int, month time.Month, day, hour, minute, second, nanosecond int) hff.Value {
		return hff.Timestamp(time.Date(year, month, day, hour, minute, second, nanosecond, time.UTC))
	}
	rows := []struct {
		created, updated hff.Value
		createdSQL       string
		createdRemainder int
		updatedSQL       string
	}{
		{hff.Null(), hff.Null(), "", 0, ""},
		{stamp(2026, 10, 1, 0, 0, 0, 123), hff.Null(), "2026-10-01 00:00:00+00", 123, ""},
		{stamp(2026, 10, 1, 0, 0, 0, 123), stamp(2026, 10, 1, 0, 0, 0, 0), "2026-10-01 00:00:00+00", 123, "2026-10-01 00:00:00+00"},
		{stamp(2026, 10, 1, 0, 0, 0, 0), stamp(2026, 10, 1, 0, 0, 0, 0), "2026-10-01 00:00:00+00", 0, "2026-10-01 00:00:00+00"},
		{stamp(1969, 12, 31, 23, 59, 59, 999999999), stamp(1970, 1, 1, 0, 0, 0, 0), "1969-12-31 23:59:59.999999+00", 999, "1970-01-01 00:00:00+00"},
		{stamp(0, 1, 1, 0, 0, 0, 0), stamp(2026, 10, 1, 0, 0, 0, 0), "0001-01-01 00:00:00+00 BC", 0, "2026-10-01 00:00:00+00"},
		{stamp(1970, 1, 1, 0, 0, 0, 0), stamp(0, 1, 1, 0, 0, 0, 0), "1970-01-01 00:00:00+00", 0, "0001-01-01 00:00:00+00 BC"},
	}
	sources := []string{
		`created=updated`, `created!=updated`, `created<updated`, `created<=updated`,
		`created>updated`, `created>=updated`, `created:before(updated)`, `created:after(updated)`,
		`updated="2026-10-01T00:00:00.000000123Z":timestamp`,
		`updated!="2026-10-01T00:00:00.000000123Z":timestamp`,
		`updated<"2026-10-01T00:00:00.000000123Z":timestamp`,
		`updated>="2026-10-01T00:00:00.000000123Z":timestamp`,
	}
	bindings := []hffsql.Binding{
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("events", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("events", "created_ns"))),
		hffsql.BindTimestamp(hff.Path{"updated"}, hffsql.Column("events", "updated_at")),
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for i, source := range sources {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		if strings.Contains(query.Where(), "EXTRACT(EPOCH") {
			t.Fatalf("%s: physical comparison used epoch reconstruction: %s", source, query.Where())
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, value)
		}
		var values []string
		for ri, row := range rows {
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{"created": row.created, "updated": row.updated}))
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, match)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			created, createdNS, updated := "NULL::timestamptz", "NULL::smallint", "NULL::timestamptz"
			if row.createdSQL != "" {
				created = sqlFixtureString(row.createdSQL) + "::timestamptz"
				createdNS = fmt.Sprintf("%d::smallint", row.createdRemainder)
			}
			if row.updatedSQL != "" {
				updated = sqlFixtureString(row.updatedSQL) + "::timestamptz"
			}
			values = append(values, fmt.Sprintf("(%s,%s,%s)", created, createdNS, updated))
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE physical_timestamp_%d%s AS SELECT %s FROM (VALUES %s) events(created_at,created_ns,updated_at); EXECUTE physical_timestamp_%d%s;\n",
			i, parameters, query.Where(), strings.Join(values, ","), i, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: PostgreSQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}

func TestPostgreSQLNestedTimestampsMatchLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	profileType := schema.Struct(schema.Nullable(schema.Timestamp("created")))
	e, err := engine.New(
		engine.WithSchema(schema.Struct(schema.Optional(schema.Nullable(schema.Field("profile", profileType))))),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	rows := []struct {
		value     hff.Value
		present   bool
		null      bool
		instant   string
		remainder int
	}{
		{hff.Object(map[string]hff.Value{}), false, false, "", 0},
		{hff.Object(map[string]hff.Value{"profile": hff.Null()}), true, true, "", 0},
		{hff.Object(map[string]hff.Value{"profile": hff.Object(map[string]hff.Value{"created": hff.Null()})}), true, false, "", 0},
		{hff.Object(map[string]hff.Value{"profile": hff.Object(map[string]hff.Value{"created": hff.Timestamp(time.Date(2026, 10, 1, 0, 0, 0, 123, time.UTC))})}), true, false, "2026-10-01 00:00:00+00", 123},
	}
	binding := hffsql.BindStruct(hff.Path{"profile"},
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_present"), schema.BoolType)),
		hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "profile_null"), schema.BoolType)),
		hffsql.BindTimestamp(hff.Path{"created"}, hffsql.Column("records", "created_at"),
			hffsql.WithNanosecondRemainder(hffsql.Column("records", "created_ns_remainder"))))
	var script strings.Builder
	var expected []bool
	var labels []string
	for i, source := range []string{
		`profile.created=null`,
		`profile.created:after("2026-10-01T00:00:00Z":timestamp)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(binding))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, args []string
		for _, arg := range query.Args() {
			typ, value := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			args = append(args, value)
		}
		var values []string
		for ri, row := range rows {
			match, err := filter.Match(ctx, row.value)
			if err != nil {
				t.Fatal(source, ri, err)
			}
			expected = append(expected, match)
			labels = append(labels, fmt.Sprintf("%s row%d", source, ri))
			instant := "NULL::timestamptz"
			remainder := "NULL::smallint"
			if row.instant != "" {
				instant = sqlFixtureString(row.instant) + "::timestamptz"
				remainder = fmt.Sprintf("%d::smallint", row.remainder)
			}
			values = append(values, fmt.Sprintf("(%t,%t,%s,%s)", row.present, row.null, instant, remainder))
		}
		parameters, arguments := "", ""
		if len(types) > 0 {
			parameters = "(" + strings.Join(types, ",") + ")"
			arguments = "(" + strings.Join(args, ",") + ")"
		}
		fmt.Fprintf(&script, "PREPARE nested_timestamp_%d%s AS SELECT %s FROM (VALUES %s) records(profile_present,profile_null,created_at,created_ns_remainder); EXECUTE nested_timestamp_%d%s;\n",
			i, parameters, query.Where(), strings.Join(values, ","), i, arguments)
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	results := strings.Fields(string(out))
	if len(results) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(results), len(expected), out)
	}
	for i, result := range results {
		if (result == "t") != expected[i] {
			t.Errorf("%s: PostgreSQL=%s local=%v", labels[i], result, expected[i])
		}
	}
}

func TestPostgreSQLTimestampRelationShapeMatchesLocal(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	itemType := schema.NullableType(schema.TimestampType)
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.Nullable(schema.List("times", itemType)),
			schema.Nullable(schema.Float("index")),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		t.Fatal(err)
	}
	stamp := hff.Timestamp(time.Date(2026, 10, 1, 0, 0, 0, 123, time.UTC))
	rows := []struct {
		times hff.Value
		index hff.Value
	}{
		{hff.List(), hff.Float(0)},
		{hff.List(stamp), hff.Float(-1)},
		{hff.List(hff.Null()), hff.Float(0)},
		{hff.Null(), hff.Float(0)},
		{hff.List(stamp, hff.Null()), hff.Float(1.5)},
		{hff.List(stamp, hff.Null()), hff.Float(2)},
		{hff.List(stamp, hff.Null()), hff.Float(-2)},
	}
	relation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("hff_times"), "item"),
		hffsql.WithCorrelation(hffsql.TrustedSQL("item.parent_id=records.id",
			hffsql.Column("item"), hffsql.Column("records"))),
		hffsql.WithRelationState(hffsql.Boolean(true),
			hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column("records", "list_null"), schema.BoolType))),
		hffsql.WithRelationItem(itemType,
			hffsql.BindTimestamp(nil, hffsql.Column("item", "created_at"),
				hffsql.WithNanosecondRemainder(hffsql.Column("item", "created_ns_remainder")))),
	)
	if err != nil {
		t.Fatal(err)
	}
	bindings := []hffsql.Binding{
		hffsql.BindRelation(hff.Path{"times"}, relation),
		hffsql.Bind(hff.Path{"index"}, hffsql.Column("records", "idx")),
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE hff_parents(id integer,list_null boolean,idx double precision);" +
		"INSERT INTO hff_parents VALUES (0,false,0),(1,false,-1),(2,false,0),(3,true,0)," +
		"(4,false,1.5),(5,false,2),(6,false,-2);" +
		"CREATE TEMP TABLE hff_times(parent_id integer,created_at timestamptz,created_ns_remainder smallint);" +
		"INSERT INTO hff_times VALUES " +
		"(1,'2026-10-01 00:00:00+00',123),(2,NULL,NULL)," +
		"(4,'2026-10-01 00:00:00+00',123),(4,NULL,NULL)," +
		"(5,'2026-10-01 00:00:00+00',123),(5,NULL,NULL)," +
		"(6,'2026-10-01 00:00:00+00',123),(6,NULL,NULL);\n")
	var expected, labels []string
	for qi, source := range []string{
		`times:size=0`, `times:has(index)`, `times:has(-1)`, `times:has(1.5)`,
		`times:any(stamp=>stamp:[timestamp]:exists)`,
		`times:any(stamp=>stamp:[int]:exists)`,
	} {
		program, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		filter, err := local.Build(ctx, program)
		if err != nil {
			t.Fatal(source, err)
		}
		query, err := hffsql.Build(ctx, program, hffsql.WithDialect(hffsql.PostgreSQL),
			hffsql.WithBindings(bindings...))
		if err != nil {
			t.Fatal(source, err)
		}
		var types, arguments []string
		for _, arg := range query.Args() {
			typ, literal := sqlFixtureParameter(t, arg)
			types = append(types, typ)
			arguments = append(arguments, literal)
		}
		allTypes := append(append([]string(nil), types...), "integer")
		parameters := "(" + strings.Join(allTypes, ",") + ")"
		name := fmt.Sprintf("timestamp_relation_%d", qi)
		fmt.Fprintf(&script, "PREPARE %s%s AS SELECT %s FROM hff_parents AS records WHERE records.id=$%d;\n",
			name, parameters, query.Where(), len(types)+1)
		for ri, row := range rows {
			match, err := filter.Match(ctx, hff.Object(map[string]hff.Value{
				"times": row.times, "index": row.index,
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
	command := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	command.Stdin = strings.NewReader(script.String())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("timestamp relation parity failed: %v\n%s", err, output)
	}
	actual := strings.Fields(string(output))
	if len(actual) != len(expected) {
		t.Fatalf("got %d results, want %d: %s", len(actual), len(expected), output)
	}
	for i, want := range expected {
		if actual[i] != want {
			t.Errorf("%s PostgreSQL=%s local=%s", labels[i], actual[i], want)
		}
	}
}
