//go:build integration

package sql_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

// PostgreSQL's Float decoder may fail where the JSON token remains valid.
// A typed map materializes ordinary null before the next stage can observe it.
func TestPostgreSQLItemMapPipelineFloatBoundaries(t *testing.T) {
	container := os.Getenv("HFF_POSTGRES_CONTAINER")
	if container == "" {
		t.Fatal("integration tests require HFF_POSTGRES_CONTAINER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Map("labels", schema.AnyType))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	var script strings.Builder
	var expected []bool
	var labels []string
	for _, token := range []string{"1e-400", "-1e-400", "1e400", "1.0"} {
		invalid := token != "1.0"
		for _, tc := range []struct {
			source string
			array  bool
			want   bool
		}{
			{`labels:items:map(p=>p[1]:[float]):filter(v=>v=null):map(v=>v:type):contains("null")`, false, invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):filter(v=>v=null):map(v=>v:type):contains("null")`, false, invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):concat([]):map(v=>v:type):contains("null")`, false, invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):map(v=>v:[float]):filter(v=>false):concat(null):all(v=>true)=null`, false, !invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):filter(v=>false):concat([]):map(v=>v!):all(v=>true)`, false, true},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):map(v=>v:[float]):filter(v=>false):concat([]):all(v=>true)`, false, !invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[list[float]]):concat([]):map(v=>v[0]:type):contains("null")`, true, invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):map(v=>v:[float]):filter(v=>false):all(v=>true)`, false, !invalid},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):filter(v=>false):map(v=>v!):all(v=>true)`, false, true},
			{`labels:items:map(p=>p[1]):concat([]):map(v=>v:[float]):map(v=>[v]):map(v=>v[0]):map(v=>v:type):contains("null")`, false, invalid},
			{`labels:items:map(p=>p[1]:[float]):filter(v=>v!=null):map(v=>v!):count(v=>true)=0`, false, invalid},
			{`labels:items:map(p=>p[1]:[float]):filter(v=>false):map(v=>v!:type):exists`, false, true},
			{`labels:items:map(p=>p[1]:[float]!):filter(v=>false):all(v=>true)`, false, !invalid},
			{`labels:items:map(p=>p[1]:[float]):filter(v=>v:[float]:exists):map(v=>v:type):contains("number")`, false, !invalid},
			{`labels:items:map(p=>p[1]:[float]):map(v=>v:type):contains("null")`, false, invalid},
			{`labels:items:map(p=>p[1]:[float]!):any(v=>true)`, false, !invalid},
			{`labels:items:map(p=>p[1]:[float]):any(v=>v:[null]=null)`, false, invalid},
			{`labels:items:map(p=>p[1]:[float]):map(v=>v!):exists`, false, !invalid},
			{`labels:items:map(p=>p[1]:[float]):map(v=>v:[float]):exists`, false, !invalid},
			{`labels:items:map(p=>p[1]:[float]):any(v=>v=null)`, false, invalid},
			{`labels:items:map(p=>p[1]:[float]+0):any(v=>v=null)`, false, invalid},
			{`labels:items:map(p=>p[1]:[float]):map(v=>v:json):contains("null")`, false, invalid},
			{`labels:items:map(p=>p[1]):map(v=>v:type):contains("number")`, false, true},
			{`labels:items:map(p=>p[1]:[list[float]]):map(v=>v[0]:type):contains("null")`, true, invalid},
			{`labels:items:map(p=>p[1]:[list[float]]):map(v=>v[0]!):any(v=>true)`, true, !invalid},
			{`labels:items:map(p=>p[1]:[list[float]]):map(v=>v:[list[float]]):exists`, true, !invalid},
			{`labels:items:map(p=>p[1]:[list[float]]:concat([])):map(v=>v:[list[float]]):exists`, true, !invalid},
		} {
			p, err := e.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"labels"}, hffsql.Column("records", "labels"), hffsql.JSON)))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			value := token
			if tc.array {
				value = "[" + value + "]"
			}
			raw := `{"n":` + value + `}`
			appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(raw)+"::json)) records(labels)", len(expected))
			expected = append(expected, tc.want)
			labels = append(labels, fmt.Sprintf("%s %s", token, tc.source))
		}
	}
	// A statically nonnullable Float can still become ordinary null when
	// its raw JSON number cannot be decoded by the backend's Float model.
	// An identical guard must test that decoded value, not just its schema.
	scalar, err := engine.New(engine.WithSchema(schema.Struct(schema.Float("x"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"1e-400", "-1e-400", "1e400", "1.0"} {
		invalid := token != "1.0"
		for _, tc := range []struct {
			source string
			want   bool
		}{
			{`x=null`, invalid},
			{`x:[float]=null`, false},
			{`x:[float]:exists`, !invalid},
			{`x!:exists`, !invalid},
			{`x!:type="null"`, false},
		} {
			p, err := scalar.Compile(ctx, tc.source)
			if err != nil {
				t.Fatal(tc.source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.BindJSON(hff.Path{"x"}, hffsql.Column("records", "x"), hffsql.JSON)))
			if err != nil {
				t.Fatal(tc.source, err)
			}
			appendAggregateProfileQuery(t, &script, q, "(VALUES ("+sqlFixtureString(token)+"::json)) records(x)", len(expected))
			expected = append(expected, tc.want)
			labels = append(labels, fmt.Sprintf("%s %s", token, tc.source))
		}
	}
	runSQLBooleanParityScript(t, ctx, container, script.String(), expected, labels)
}
