package sql_test

import (
	"context"
	"strings"
	"testing"

	hffsql "github.com/ornew/hff/backends/sql"
)

// These expressions require general lowering, rather than the direct WHERE
// shortcut. Their shape checks keep identity views from regaining barriers.
func TestNativeIdentityViewsAndPresenceShape(t *testing.T) {
	ctx := context.Background()
	for _, mode := range []string{"nullable", "required"} {
		e, mappings := nativeColumnEngine(t, mode)
		for _, fixture := range []struct {
			source   string
			barriers int
			mustHave string
		}{
			{`i:round:json=i:floor:json`, 3, `"records"."i"::text`},
			{`i:ceil:floor:round:json=i:json`, 3, `"records"."i"::text`},
			{`d:nanoseconds:json=i:json`, 3, `"records"."d"::text`},
			{`d!:nanoseconds:json=d:nanoseconds:json`, 4, `"records"."d"`},
			{`data.n:[int]:round:json=i:json`, 9, `json_typeof`},
			{`items:any(x=>x:round+1>i)`, 9, `json_array_elements`},
			{`i+2>10`, 3, `"records"."i"`},
			{`p:and(q):type="bool"`, 3, `"records"."p"`},
		} {
			p, err := e.Compile(ctx, fixture.source)
			if err != nil {
				t.Fatal(mode, fixture.source, err)
			}
			q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
			if err != nil {
				t.Fatal(mode, fixture.source, err)
			}
			if count := strings.Count(q.Where(), "OFFSET 0"); count != fixture.barriers {
				t.Errorf("%s %s: %d barriers, want %d: %s", mode, fixture.source, count, fixture.barriers, q.Where())
			}
			if !strings.Contains(q.Where(), fixture.mustHave) || strings.Contains(q.Where(), " IS NULL OR TRUE") {
				t.Errorf("%s %s: missing carrier or retained tautological presence: %s", mode, fixture.source, q.Where())
			}
			if mode == "nullable" && fixture.source == `d!:nanoseconds:json=d:nanoseconds:json` && !strings.Contains(q.Where(), "IS NOT NULL") {
				t.Errorf("nullable Duration guard lost its presence test: %s", q.Where())
			}
			assertOperationArgumentsUsed(t, q)
		}
		p, err := e.Compile(ctx, `f:round:json=f:json`)
		if err != nil {
			t.Fatal(err)
		}
		q, err := hffsql.Build(ctx, p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(mappings...))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(q.Where(), "round(") || strings.Count(q.Where(), "OFFSET 0") <= 3 {
			t.Fatal(mode, "Float round lost its computation", q.Where())
		}
	}
}
