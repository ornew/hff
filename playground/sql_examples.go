package main

import (
	"github.com/ornew/hff"
	"github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/config"
	"github.com/ornew/hff/engine"
)

type sqlExample struct {
	ID            string `json:"id"`
	Label         string `json:"label"`
	Query         string `json:"query"`
	Configuration string `json:"configuration"`
}

// Generate visible examples with the public constructors, so their JSON always
// uses the same contract as configurations supplied by applications.
func sqlExamples() ([]sqlExample, error) {
	people := hff.NewStructType(
		hff.Field{Name: "name", Type: hff.PrimitiveType(hff.StringType)},
		hff.Field{Name: "age", Type: hff.PrimitiveType(hff.IntType)},
	)
	labels := hff.NewStructType(
		hff.Field{Name: "service", Type: hff.PrimitiveType(hff.StringType)},
		hff.Field{Name: "labels", Type: hff.NewMapType(hff.PrimitiveType(hff.StringType))},
	)
	itemType := hff.NewStructType(
		hff.Field{Name: "price", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "active", Type: hff.PrimitiveType(hff.BoolType)},
	)
	orders := hff.NewStructType(
		hff.Field{Name: "id", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "items", Type: hff.NewListType(itemType)},
	)
	timestamps := hff.NewStructType(
		hff.Field{Name: "id", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "created", Type: hff.PrimitiveType(hff.TimestampType)},
	)
	profiles := hff.NewStructType(
		hff.Field{Name: "id", Type: hff.PrimitiveType(hff.IntType)},
		hff.Field{Name: "profile", Type: hff.NewStructType(
			hff.Field{Name: "name", Type: hff.PrimitiveType(hff.StringType)}), Optional: true},
	)
	item := sql.BindStruct(nil, sql.Boolean(true), sql.Boolean(false),
		sql.Bind(hff.Path{"price"}, sql.Column("item", "price")),
		sql.Bind(hff.Path{"active"}, sql.Column("item", "active")))
	relation, err := sql.NewRelation(
		sql.WithRelationSource(sql.Column("order_items"), "item"),
		sql.WithCorrelation(sql.Equal(
			sql.ColumnValue(sql.Column("item", "order_id"), hff.PrimitiveType(hff.IntType)),
			sql.ColumnValue(sql.Column("orders", "id"), hff.PrimitiveType(hff.IntType)))),
		sql.WithRelationItem(itemType, item),
	)
	if err != nil {
		return nil, err
	}
	inputs := []struct {
		id, label, query string
		schema           hff.Type
		bindings         []sql.Binding
		caps             capabilities.Set
	}{
		{"people", "People · columns", `age>=18 name="Alice"`, people,
			[]sql.Binding{sql.Bind(hff.Path{"name"}, sql.Column("people", "name")), sql.Bind(hff.Path{"age"}, sql.Column("people", "age"))},
			capabilities.None().Allow(capabilities.Logical, capabilities.Comparison, capabilities.Access)},
		{"labels", "Services · JSONB labels", `labels.app="auth"`, labels,
			[]sql.Binding{sql.Bind(hff.Path{"service"}, sql.Column("services", "name")), sql.BindJSON(hff.Path{"labels"}, sql.Column("services", "labels"), sql.JSONB)},
			capabilities.None().Allow(capabilities.Comparison, capabilities.Access)},
		{"orders", "Orders · related items", `items:any(item=>item.price>100 item.active)`, orders,
			[]sql.Binding{sql.Bind(hff.Path{"id"}, sql.Column("orders", "id")), sql.BindRelation(hff.Path{"items"}, relation)},
			capabilities.None().Allow(capabilities.Logical, capabilities.Comparison, capabilities.Access,
				capabilities.Callbacks, capabilities.Method("list.any"))},
		{"timestamps", "Events · nanosecond timestamps", `created:year=2024`, timestamps,
			[]sql.Binding{sql.Bind(hff.Path{"id"}, sql.Column("events", "id")),
				sql.BindTimestamp(hff.Path{"created"}, sql.Column("events", "created_at"),
					sql.WithNanosecondRemainder(sql.Column("events", "created_ns")))},
			capabilities.None().Allow(capabilities.Comparison, capabilities.Access, capabilities.Method("timestamp.year"))},
		{"profiles", "Customers · flattened profile", `profile.name="Alice"`, profiles,
			[]sql.Binding{sql.Bind(hff.Path{"id"}, sql.Column("customers", "id")),
				sql.BindStruct(hff.Path{"profile"},
					sql.IsNotNull(sql.ColumnValue(sql.Column("customers", "profile_id"), hff.PrimitiveType(hff.IntType))),
					sql.Boolean(false),
					sql.Bind(hff.Path{"name"}, sql.Column("customers", "profile_name")))},
			capabilities.None().Allow(capabilities.Comparison, capabilities.Access)},
	}
	out := make([]sqlExample, 0, len(inputs))
	for _, input := range inputs {
		data, err := config.Encode(engine.Config{Schema: input.schema, Capabilities: input.caps},
			sql.Config{Dialect: sql.PostgreSQL, Bindings: input.bindings}, config.Registry{})
		if err != nil {
			return nil, err
		}
		out = append(out, sqlExample{ID: input.id, Label: input.label, Query: input.query, Configuration: string(data)})
	}
	return out, nil
}
