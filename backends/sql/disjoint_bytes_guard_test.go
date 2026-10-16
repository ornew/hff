package sql_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func disjointBytesGuardSources() []string {
	return []string{
		`value:[bytes]:base64="YQ=="`,
		`value:[bytes]:base64r="YQ"`,
		`value:[bytes]:len=1`,
		`value:[bytes]:string="YQ=="`,
		`value:[bytes]:json="\"YQ==\""`,
		`!value:[bytes]:exists`,
		`value:[bytes|null]:type="null"`,
		`value:[bytes]:base64="YQ==" | value:exists`,
		`value:exists | value:[bytes]:base64="YQ=="`,
		`value:[bytes]!:base64="YQ=="`,
	}
}

func disjointBytesGuardFixtures() []struct {
	name, sql string
	typ       hff.Type
	value     hff.Value
} {
	return []struct {
		name, sql string
		typ       hff.Type
		value     hff.Value
	}{
		{"int", "7::bigint", schema.IntType, hff.Int(7)},
		{"float", "2.5::double precision", schema.FloatType, hff.Float(2.5)},
		{"bool", "FALSE::boolean", schema.BoolType, hff.Bool(false)},
		{"string", "'sample'::text", schema.StringType, hff.String("sample")},
		{"duration", "7::bigint", schema.DurationType, hff.Duration(7)},
		{"timestamp", "'2000-01-01T00:00:00Z'::timestamptz", schema.TimestampType, hff.Timestamp(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))},
		{"bytes", "decode('61','hex')", schema.BytesType, hff.Bytes([]byte("a"))},
	}
}

func disjointBytesGuardEngine(t *testing.T, typ hff.Type) *engine.Engine {
	t.Helper()
	e, err := engine.New(engine.WithSchema(schema.Struct(schema.Nullable(schema.Field("value", typ)))), engine.WithCapabilities(capabilities.Standard()))
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func disjointBytesGuardBinding(typ hff.Type) hffsql.Binding {
	column := hffsql.Column("records", "value")
	if typ.Kind() == hff.TimestampType {
		return hffsql.BindTimestamp(hff.Path{"value"}, column)
	}
	return hffsql.Bind(hff.Path{"value"}, column)
}

func TestDisjointBytesGuardKeepsByteaCarrier(t *testing.T) {
	for _, fixture := range disjointBytesGuardFixtures() {
		e := disjointBytesGuardEngine(t, fixture.typ)
		for i, source := range disjointBytesGuardSources() {
			p, err := e.Compile(context.Background(), source)
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			q, err := hffsql.Build(context.Background(), p, hffsql.WithDialect(hffsql.PostgreSQL), hffsql.WithBindings(disjointBytesGuardBinding(fixture.typ)))
			if err != nil {
				t.Fatal(fixture.name, source, err)
			}
			if fixture.name != "bytes" && i < 5 && !strings.Contains(q.Where(), "NULL::bytea") {
				t.Fatal(fixture.name, source, "missing bytea carrier", q.Where())
			}
			requireConcatArgsUsed(t, q)
		}
	}
}
