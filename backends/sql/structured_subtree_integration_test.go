//go:build integration

package sql_test

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"reflect"
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

func TestPostgreSQLStructuredMetadataSubtrees(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("set HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	docType := schema.NullableType(schema.MapOf(schema.AnyType))
	innerType := schema.Struct(schema.Field("doc", docType))
	profileType := schema.Struct(schema.Optional(schema.Nullable(schema.Field("inner", innerType))))
	states := func(alias, name string) hffsql.SQLPredicate {
		return hffsql.AsPredicate(hffsql.ColumnValue(hffsql.Column(alias, name), schema.NullableType(schema.BoolType)))
	}
	jsonBinding := func(alias string) hffsql.Binding {
		return hffsql.BindJSON(hff.Path{"doc"}, hffsql.Column(alias, "doc"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column(alias, "metadata")))
	}
	profileBinding := func(alias string, path hff.Path) hffsql.Binding {
		return hffsql.BindStruct(path, states(alias, "profile_present"), states(alias, "profile_null"),
			hffsql.BindStruct(hff.Path{"inner"}, states(alias, "inner_present"), states(alias, "inner_null"), jsonBinding(alias)))
	}
	type row struct {
		profilePresent, profileNull, innerPresent, innerNull bool
		doc                                                  hff.Value
	}
	payload := func(price hff.Value) hff.Value {
		return hff.Object(map[string]hff.Value{"": hff.Float(math.Copysign(0, -1)), "nested": hff.Object(map[string]hff.Value{"price": price}), "a/b~": hff.Object(map[string]hff.Value{"日": hff.Float(math.Copysign(0, -1))})})
	}
	hidden := payload(hff.Float(math.Copysign(0, -1)))
	rows := []row{
		{false, false, true, false, hidden}, {true, true, true, false, hidden}, {true, false, false, false, hidden}, {true, false, true, true, hidden},
		{true, false, true, false, hff.Null()}, {true, false, true, false, hff.Object(nil)},
		{true, false, true, false, hff.Object(map[string]hff.Value{"nested": hff.Null()})},
		{true, false, true, false, payload(hff.Int(0))}, {true, false, true, false, hidden},
		{true, false, true, false, payload(hff.Float(math.SmallestNonzeroFloat64))},
		{true, false, true, false, payload(hff.String("wrong"))}, {true, false, true, false, payload(hff.Bool(false))},
	}
	profileValue := func(r row) hff.Value {
		if !r.profilePresent || r.profileNull {
			return hff.Null()
		}
		fields := map[string]hff.Value{}
		if r.innerPresent {
			fields["inner"] = hff.Null()
			if !r.innerNull {
				fields["inner"] = hff.Object(map[string]hff.Value{"doc": r.doc})
			}
		}
		return hff.Object(fields)
	}
	var script strings.Builder
	script.WriteString("CREATE TEMP TABLE records(id bigint,profile_present boolean,profile_null boolean,inner_present boolean,inner_null boolean,doc jsonb,metadata jsonb); CREATE TEMP TABLE children(parent_id bigint,profile_present boolean,profile_null boolean,inner_present boolean,inner_null boolean,doc jsonb,metadata jsonb);\n")
	for i, r := range rows {
		fmt.Fprintf(&script, "INSERT INTO records VALUES(%d,%t,%t,%t,%t,%s::jsonb,%s::jsonb);\n", i, r.profilePresent, r.profileNull, r.innerPresent, r.innerNull, sqlFixtureString(metadataJSONFixture(t, r.doc)), sqlFixtureString(pairTypeMetadata(t, r.doc)))
	}
	script.WriteString("INSERT INTO children SELECT * FROM records;\n")
	type mode struct {
		name     string
		typ      hff.Type
		bindings []hffsql.Binding
		record   func(row) hff.Value
		prefix   string
	}
	flatSchema := schema.Struct(schema.Optional(schema.Nullable(schema.Field("profile", profileType))))
	modes := []mode{{"flattened", flatSchema, []hffsql.Binding{profileBinding("records", hff.Path{"profile"})}, func(r row) hff.Value {
		fields := map[string]hff.Value{}
		if r.profilePresent {
			fields["profile"] = profileValue(r)
		}
		return hff.Object(fields)
	}, "profile.inner.doc"}}
	for _, structured := range []bool{false, true} {
		itemType, itemBinding, prefix := docType, hffsql.BindJSON(nil, hffsql.Column("child", "doc"), hffsql.JSONB, hffsql.WithTypeMetadata(hffsql.Column("child", "metadata"))), "x"
		if structured {
			itemType = schema.NullableType(profileType)
			itemBinding = profileBinding("child", nil)
			prefix = "x.inner.doc"
		}
		relation, err := hffsql.NewRelation(hffsql.WithRelationSource(hffsql.Column("children"), "child"), hffsql.WithCorrelation(hffsql.Equal(hffsql.ColumnValue(hffsql.Column("child", "parent_id"), schema.IntType), hffsql.ColumnValue(hffsql.Column("records", "id"), schema.IntType))), hffsql.WithRelationItem(itemType, itemBinding))
		if err != nil {
			t.Fatal(err)
		}
		isStructured := structured
		modes = append(modes, mode{fmt.Sprintf("relation_%t", structured), schema.Struct(schema.List("items", itemType)), []hffsql.Binding{hffsql.BindRelation(hff.Path{"items"}, relation)}, func(r row) hff.Value {
			v := r.doc
			if isStructured {
				v = profileValue(r)
			}
			return hff.Object(map[string]hff.Value{"items": hff.List(v)})
		}, prefix})
	}
	var expected [][]bool
	var labels []string
	for _, m := range modes {
		e, err := engine.New(engine.WithSchema(m.typ), engine.WithCapabilities(capabilities.Standard()))
		if err != nil {
			t.Fatal(err)
		}
		suffixes := []string{".``:string=\"-0.0\"", `.nested.price=0`, `.nested.price=null`, `.nested.price:string="null"`, `.nested.price:[float]:string="-0.0"`, `.nested.price:[float]:string="5e-324"`, ".`a/b~`.`日`:string=\"-0.0\"", `.nested!:exists`, `.nested.price!:exists`, `.nested:json="null"`, `.nested.price:string="wrong"`}
		sources := []string{}
		for _, suffix := range suffixes {
			source := m.prefix + suffix
			if m.name != "flattened" {
				source = "items:any(x=>" + source + ")"
			}
			sources = append(sources, source)
		}
		observed := "(" + m.prefix + ".nested.price=0):type=\"null\""
		if m.name != "flattened" {
			observed = "items:any(x=>" + observed + ")"
		}
		sources = append(sources, observed)
		if m.name == "flattened" {
			sources = append(sources, `profile!.inner!.doc!.nested.price:exists`)
		} else {
			inner := strings.Replace(m.prefix, "x", "y", 1)
			sources = append(sources, "items:any(x=>items:any(y=>"+m.prefix+`.nested.price:string="-0.0" `+inner+`.nested.price=0))`, "items:any(x=>items:any(x=>"+m.prefix+".`a/b~`.`日`:string=\"-0.0\"))")
		}
		for _, source := range sources {
			p, err := e.Compile(ctx, source)
			if err != nil {
				t.Fatal(m.name, source, err)
			}
			f, err := local.Build(ctx, p)
			if err != nil {
				t.Fatal(source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(m.bindings...))
			if err != nil {
				t.Fatal(source, err)
			}
			var want []bool
			for _, r := range rows {
				got, err := f.Match(ctx, m.record(r))
				if err != nil {
					t.Fatal(source, err)
				}
				want = append(want, got)
			}
			expected = append(expected, want)
			labels = append(labels, m.name+" "+source)
			var types, args []string
			for _, arg := range q.Args() {
				typ, literal := sqlFixtureParameter(t, arg)
				types = append(types, typ)
				args = append(args, literal)
			}
			params, arguments := "", ""
			if len(types) > 0 {
				params = "(" + strings.Join(types, ",") + ")"
				arguments = "(" + strings.Join(args, ",") + ")"
			}
			id := len(expected)
			fmt.Fprintf(&script, "PREPARE subtree_%d%s AS SELECT json_agg(%s ORDER BY id) FROM records; EXECUTE subtree_%d%s;\n", id, params, q.Where(), id, arguments)
		}
	}
	cmd := exec.CommandContext(ctx, "docker", "exec", "-i", container, "psql", "-X", "-q", "-A", "-t", "-v", "ON_ERROR_STOP=1", "-U", "postgres")
	cmd.Stdin = strings.NewReader(script.String())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	decoder := json.NewDecoder(strings.NewReader(string(out)))
	for i, want := range expected {
		var got []bool
		if err := decoder.Decode(&got); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: SQL%v local%v", labels[i], got, want)
		}
	}
	t.Logf("verified %d structured metadata subtree results", len(expected)*len(rows))
}
