package sql_test

import (
	"context"
	"strings"
	"testing"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func TestQualifiedNativeColumnsKeepComputedBarriers(t *testing.T) {
	ctx := context.Background()
	e, mappings := nativeColumnEngine(t, "nullable")
	for _, source := range []string{
		`i+1>j`, `i=f`, `i!:exists`, `i!:type="null"`, `i:[int]:exists`,
		`i:json=i:json`, `[i,i]!=[]`, `f:round>1`, `s:lower=t`, `p:and(q):type="bool"`,
		`b:len=c:len`, `items:any(x=>i:exists)`,
		`items:map(x=>i+x):size>0`, `items:foldl(i,(a,x)=>a+x)>0`,
	} {
		p, err := e.Compile(ctx, source)
		if err != nil {
			t.Fatal(source, err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if err != nil {
			t.Fatal(source, err)
		}
		for _, name := range []string{"i", "j", "f", "g", "s", "t", "p", "q", "b", "c", "d", "e"} {
			if strings.Contains(q.Where(), `SELECT "records"."`+name+`" AS v OFFSET 0`) {
				t.Fatal(source, "retained qualified native-column projection", q.Where())
			}
		}
		if !strings.Contains(q.Where(), "OFFSET 0") {
			t.Fatal(source, "lost computed or guard-presence barriers", q.Where())
		}
	}
}

func TestQualifiedNativeColumnReferenceUsesLiteralComponents(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("profile", schema.Struct(schema.Int("age")))), schema.Int("v"))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	// A nullable struct needs parent-state handling, so bind it explicitly.
	p, err := e.Compile(ctx, `v+1>0 profile.age:exists`)
	if err != nil {
		t.Fatal(err)
	}
	q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(
		hffsql.Bind(hff.Path{"v"}, hffsql.Column("__hff_1", `odd".column`)),
		hffsql.BindStruct(hff.Path{"profile"},
			hffsql.TrustedSQL(`"__hff_1"."profile_present"`, hffsql.Column("__hff_1", "profile_present")),
			hffsql.TrustedSQL(`"__hff_1"."profile_null"`, hffsql.Column("__hff_1", "profile_null")),
			hffsql.Bind(hff.Path{"age"}, hffsql.Column("__hff_1", "age"))),
	))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(q.Where(), `"__hff_1"."odd"".column"`) || strings.Contains(q.Where(), `AS "__hff_1"`) {
		t.Fatal("qualified caller identifier was altered or shadowed", q.Where())
	}
	if strings.Contains(q.Where(), `SELECT "__hff_1"."odd"".column" AS v`) {
		t.Fatal("raw native column was projected", q.Where())
	}
	if !strings.Contains(q.Where(), `"__hff_1"."profile_present"`) || !strings.Contains(q.Where(), `"__hff_1"."profile_null"`) {
		t.Fatal("structured-parent state was discarded", q.Where())
	}
}

func TestUnqualifiedNativeColumnsRetainTheirBindingProjection(t *testing.T) {
	ctx := context.Background()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Int("i")))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	p, err := e.Compile(ctx, `i+1>0`)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"v", "p", "value", "ord", "records.i", `odd".column`} {
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(hffsql.Bind(hff.Path{"i"}, hffsql.Column(name))))
		if err != nil {
			t.Fatal(name, err)
		}
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if strings.Count(q.Where(), "SELECT "+quoted+" AS v OFFSET 0") != 1 {
			t.Fatal(name, "unqualified literal component lost its original binding projection", q.Where())
		}
	}
}
