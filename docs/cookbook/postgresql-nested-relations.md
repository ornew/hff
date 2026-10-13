# Filter Nested Order Relations

This recipe checks a condition on an order item and a condition on one of that
item's adjustments. Nested relations keep the relationship between those rows
explicit, so a qualifying adjustment from a different item cannot satisfy the
filter.

## Tables and sample rows

```sql
CREATE TABLE orders (
    id bigint PRIMARY KEY,
    state text NOT NULL
);
CREATE TABLE order_items (
    id bigint PRIMARY KEY,
    order_id bigint NOT NULL REFERENCES orders(id),
    price bigint NOT NULL
);
CREATE TABLE item_adjustments (
    id bigint PRIMARY KEY,
    order_item_id bigint NOT NULL REFERENCES order_items(id),
    amount bigint NOT NULL
);

INSERT INTO orders VALUES (101, 'open'), (102, 'open'), (103, 'open');
INSERT INTO order_items VALUES
    (11, 101, 125),
    (21, 102, 125),
    (22, 102, 80),
    (31, 103, 80);
INSERT INTO item_adjustments VALUES
    (1, 11, 5),
    (2, 21, -5),
    (3, 22, 10),
    (4, 31, 10);
```

The logical shape is `orders.items[].{price,adjustments[]}`. Order 101 has an
item over 100 with a positive adjustment. Order 102 has an expensive item and
a separately adjusted cheaper item; no single item satisfies both checks.
Order 103 has an adjustment, but its item costs less than 100.

For order 101, the reconstructed value is:

```json
{"state":"open","items":[{"price":125,"adjustments":[5]}]}
```

## Bind both relation levels

The function below declares the record shape, correlates the nested relation
to its current parent item, builds the predicate, and executes it through the
caller's database handle:

```go
package main

import (
	"context"
	"database/sql"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func matchingOrders(ctx context.Context, db *sql.DB) ([]int64, error) {
	itemType := schema.Struct(
		schema.Int("price"),
		schema.List("adjustments", schema.IntType),
	)
	e, err := engine.New(
		engine.WithSchema(schema.Struct(
			schema.String("state"),
			schema.List("items", itemType),
		)),
		engine.WithCapabilities(capabilities.Standard()),
	)
	if err != nil {
		return nil, err
	}
	program, err := e.Compile(ctx,
		`state="open" items:any(item=>item.price>100 item.adjustments:any(amount=>amount>0))`,
	)
	if err != nil {
		return nil, err
	}
	adjustmentRelation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("item_adjustments"), "adjustment"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("adjustment", "order_item_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("item", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(schema.IntType,
			hffsql.Bind(hff.Path{}, hffsql.Column("adjustment", "amount")),
		),
	)
	if err != nil {
		return nil, err
	}
	itemBinding := hffsql.BindStruct(
		hff.Path{}, hffsql.Boolean(true), hffsql.Boolean(false),
		hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
		hffsql.BindRelation(hff.Path{"adjustments"}, adjustmentRelation),
	)
	itemsRelation, err := hffsql.NewRelation(
		hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
		hffsql.WithCorrelation(hffsql.Equal(
			hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
			hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
		)),
		hffsql.WithRelationItem(itemType, itemBinding),
	)
	if err != nil {
		return nil, err
	}
	query, err := hffsql.Build(ctx, program,
		hffsql.WithDialect(hffsql.PostgreSQL),
		hffsql.WithBindings(
			hffsql.Bind(hff.Path{"state"}, hffsql.Column("orders", "state")),
			hffsql.BindRelation(hff.Path{"items"}, itemsRelation),
		),
	)
	if err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx,
		`SELECT orders.id FROM orders WHERE `+query.Where(), query.Args()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return ids, nil
}
```

The caller opens and configures `db`; HFF does not choose a driver or open a
connection. The query returns `[]int64{101}` for the sample rows.

The outer relation reads `order_items`; the nested relation reads
`item_adjustments`. The second correlation uses the current item's ID rather
than the order ID. HFF does not infer either foreign key or generate a join.
The caller runs the complete statement and passes `Args()` separately to the
driver. See the [relation guide](../backends/postgresql/relations.md#nest-a-one-to-many-relation)
for ordering and materialization limits.
