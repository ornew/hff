# PostgreSQL Relation Bindings

`BindRelation` maps one HFF list field to rows from one caller-selected
PostgreSQL table or view. A relation has four parts: the HFF list path, a row
source and alias, a correlation predicate that connects each row to the outer
record, and a binding for one row as one HFF list item. HFF does not
inspect foreign keys, choose tables, join tables, or execute the resulting
query. The service supplies the surrounding `SELECT`, connection, and
execution context.

## Start with the database shape

Suppose an order has zero or more items. The parent query selects from
`orders`; each related row comes from `order_items`, joined logically by
`order_items.order_id = orders.id`:

```sql
CREATE TABLE orders (
    id bigint PRIMARY KEY,
    status text NOT NULL
);

CREATE TABLE order_items (
    id bigint PRIMARY KEY,
    order_id bigint NOT NULL REFERENCES orders(id),
    price bigint NOT NULL,
    active boolean NOT NULL
);

ALTER TABLE orders
    ADD COLUMN tenant_id bigint NOT NULL DEFAULT 17;

INSERT INTO orders (id, status) VALUES (42, 'open');
INSERT INTO order_items (id, order_id, price, active) VALUES
    (7, 42, 125, true),
    (8, 42, 35, false);
```

The logical HFF schema describes the record seen by the query. For this
example it has one field, `items`, whose list elements have `price` and
`active` fields:

```go
itemType := schema.Struct(
    schema.Int("price"),
    schema.Bool("active"),
)
recordType := schema.Struct(schema.List("items", itemType))
```

If the local backend received the row data, its HFF record would look like
this:

```json
{"items":[{"price":125,"active":true},{"price":35,"active":false}]}
```

The PostgreSQL backend reconstructs that logical list from correlated rows;
there does not need to be an `items` column in `orders`. The root relation
binding uses `hff.Path{"items"}`. The item binding uses an empty path because
its paths are relative to one `order_items` row.
The table may contain columns such as `orders.status` that the HFF schema does
not expose; only fields the filter reads need to be represented in its schema
and bindings.

## Bind the row source and compile a filter

This complete configuration maps the child row columns into the declared
HFF item struct, then attaches that relation at `items`:

```go
item := schema.Struct(
    schema.Int("price"),
    schema.Bool("active"),
)
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.List("items", item))),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil {
    return err
}

program, err := e.Compile(ctx,
    `items:any(item=>item.price>100 item.active)`,
)
if err != nil {
    return err
}

parentOrder := hffsql.Equal(
    hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
    hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
)
itemBinding := hffsql.BindStruct(
    hff.Path{}, // the current order_items row is the HFF list item
    hffsql.Boolean(true),
    hffsql.Boolean(false),
    hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
    hffsql.Bind(hff.Path{"active"}, hffsql.Column("item", "active")),
)
relation, err := hffsql.NewRelation(
    hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
    hffsql.WithCorrelation(parentOrder),
    hffsql.WithRelationItem(item, itemBinding),
)
if err != nil {
    return err
}

query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindRelation(
        hff.Path{"items"}, relation,
    )),
)
if err != nil {
    return err
}
```

The relation's `parentOrder` predicate compares two declared integer columns
through the typed `Equal` constructor. The child field bindings are relative
to `item`:
`hff.Path{"price"}` means the HFF item field, while
`Column("item", "price")` means the SQL column `item.price`. A path such as
`hff.Path{"items", "price"}` would instead address a nested HFF field from
the outer record and is not the item-binding path.

## Build correlation predicates safely

`WithCorrelation` and `WithRelationState` accept `SQLPredicate`. Prefer the
typed constructors over raw SQL:

- `ColumnValue(column, type)` declares the HFF scalar type of a physical SQL
  column.
- `Literal(value)` adds a typed scalar value as a bound parameter.
- `Equal`, `IsNull`, and `IsNotNull` compare or test scalar expressions.
- `AsPredicate` uses a Boolean scalar; `Boolean` creates a fixed true/false
  predicate.
- `And`, `Or`, and `Not` combine predicates.

The relation example uses `Equal` to compare `item.order_id` and `orders.id`.
The caller declares both as `schema.IntType`; the backend checks the types and
quotes the identifiers. To add a tenant condition while keeping the tenant
value out of SQL text, compose typed predicates:

```go
tenantMatch := hffsql.Equal(
    hffsql.ColumnValue(hffsql.Column("orders", "tenant_id"), schema.IntType),
    hffsql.Literal(hff.Int(17)),
)
correlation := hffsql.And(parentOrder, tenantMatch)
```

Here `parentOrder` is the `Equal` predicate defined for the preceding
relation. The value `17` is emitted in `query.Args()`. Use `IsNull` and
`IsNotNull` for null tests: SQL equality with null is unknown, not an identity
test.

`TrustedSQL` remains an escape hatch for a PostgreSQL Boolean expression the
typed constructors cannot express. It accepts fixed, developer-authored SQL
text; HFF does not parse it, convert it into an AST, validate its Boolean
semantics, prove its determinism or purity, or sanitize it. Its optional
`ColumnRef` arguments support alias bookkeeping only; they do not interpolate
values or validate that the text refers to those identifiers. The service must
write and review the expression, keep untrusted query/record data out of it,
and ensure it has the intended meaning. Any expression used for state or
correlation must be deterministic and side-effect-free; HFF cannot prove that
contract. `Build` does not execute it. PostgreSQL evaluates the predicate only
when the caller runs the complete statement.

`WithRelationState(present, null)` uses the same predicate constructors to
represent a missing or null HFF list. Without it, the relation is present and
non-null. No matching rows produce an empty list; that is different from a
missing field or a null list.

## Put the predicate in the caller's statement

`Build` returns a predicate fragment and ordered arguments, not a complete
query. The caller chooses the parent row and supplies the outer alias used by
the correlation:

```go
rows, err := db.QueryContext(ctx,
    `SELECT "orders"."id" FROM "orders" WHERE `+query.Where(),
    query.Args()...,
)
```

The parameter values must be passed separately; do not interpolate
`query.Args()` into the SQL text. The caller owns database I/O, transaction
boundaries, timeouts, cancellation during execution, and result scanning. The
SQL backend does not open a connection or run the statement.

## Which shapes fit a relation?

- **One-to-many:** one `BindRelation` maps each parent to zero or more items.
  This is the natural shape for `orders.items`.
- **Nested one-to-many:** an item's `BindStruct` can contain a child
  `BindRelation` at a relative field path. Give the nested relation its own
  row source, alias, correlation to the current item alias, item type, and
  item binding. Each relation is explicit.
- **Many-to-many:** the API does not infer or generate a join through a
  junction table. Expose the desired child rows through a PostgreSQL view and
  use that view as the single row source, or model the junction rows as one
  relation and a further collection as a nested relation when that is the HFF
  schema you want.
- **One-to-one:** use a native composite or `BindStruct` for one object. A
  relation is always an HFF list; it does not turn zero-or-one rows into a
  nullable or optional struct.

For other physical representations, choose deliberately: [`Bind`](README.md)
maps scalar columns, [`BindJSON`](json.md) maps a schema-known JSON document,
[`BindComposite`](composites.md) maps a native composite, and
[`BindStruct`](flattened-structures.md) maps fields spread across columns.
These choices determine whether the physical data can represent missing,
null, empty, and ordered values.

## Order and null rules

Relations are present and non-null by default. Zero matching child rows mean
an empty list, so `any()` is false and `all()` is true. Use
`WithRelationState(present, null)` only when the storage model also needs to
represent a missing or null list. For a struct item, `BindStruct`'s `present`
expression answers whether the item itself exists; its `null` expression
answers whether that present item is SQL null. Child columns that are SQL null
are explicit null fields. An all-null set of child columns does not imply a
missing item.

`WithUniqueOrder` supplies a typed, unique ordering tuple for operations that
can observe list order, such as indexing or returning the list. It is not
required for a plain `any()` or `all()` predicate. If a relation is transformed
or guarded, the builder checks whether the consumer still observes order and
rejects a mapping that cannot preserve it.
Duration columns can be used as typed order components with
`schema.DurationType`; their signed nanosecond values define the ordering.

For a plain relation path, `any()` and `all()` can use correlated
`EXISTS`/`NOT EXISTS`. Guards, computed lists, and alias collisions may use a
general list-materialization path instead. Neither path guarantees physical
row or callback evaluation order, local HFF runtime budgets, or an index plan.
A visible correlation merely gives PostgreSQL a predicate it may be able to
use with an appropriate index and plan.

### Nest a one-to-many relation

To expose adjustments under each order item, extend the item schema and make
the second relation correlate to the current `item` row:

```sql
CREATE TABLE item_adjustments (
    id bigint PRIMARY KEY,
    order_item_id bigint NOT NULL REFERENCES order_items(id),
    amount bigint NOT NULL
);
```

```go
adjustmentRelation, err := hffsql.NewRelation(
    hffsql.WithRelationSource(hffsql.Column("item_adjustments"), "adjustment"),
    hffsql.WithCorrelation(hffsql.Equal(
        hffsql.ColumnValue(hffsql.Column("adjustment", "order_item_id"), schema.IntType),
        hffsql.ColumnValue(hffsql.Column("item", "id"), schema.IntType),
    )),
    hffsql.WithRelationItem(schema.IntType,
        hffsql.Bind(hff.Path{}, hffsql.Column("adjustment", "amount")),
    ),
    hffsql.WithUniqueOrder(
        hffsql.Ascending(hffsql.Column("adjustment", "id"), schema.IntType),
    ),
)
if err != nil {
    return err
}

itemWithAdjustments := schema.Struct(
    schema.Int("price"),
    schema.Bool("active"),
    schema.List("adjustments", schema.IntType),
)
itemBinding := hffsql.BindStruct(
    hff.Path{}, hffsql.Boolean(true), hffsql.Boolean(false),
    hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
    hffsql.Bind(hff.Path{"active"}, hffsql.Column("item", "active")),
    hffsql.BindRelation(hff.Path{"adjustments"}, adjustmentRelation),
)
outerRelation, err := hffsql.NewRelation(
    hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
    hffsql.WithCorrelation(hffsql.Equal(
        hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
        hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
    )),
    hffsql.WithRelationItem(itemWithAdjustments, itemBinding),
)
```

The outer item binding gives the nested `adjustments` field a path relative to
one `order_items` row. The nested correlation then uses `item.id` as its
parent key. The HFF shape is
`{"items":[{"price":125,"active":true,"adjustments":[5,10]}]}`. Bind
`outerRelation` at `hff.Path{"items"}` as in the preceding example. No
foreign-key discovery or automatic nested join occurs.

### Expose a many-to-many result through a view

For example, a view can turn order-item tags into one row source with the
columns needed by the HFF item binding:

```sql
CREATE TABLE tags (id bigint PRIMARY KEY, label text NOT NULL);
CREATE TABLE order_item_tags (
    order_item_id bigint NOT NULL REFERENCES order_items(id),
    tag_id bigint NOT NULL REFERENCES tags(id),
    PRIMARY KEY (order_item_id, tag_id)
);
CREATE VIEW order_tags AS
SELECT DISTINCT oi.order_id, t.id, t.label
FROM order_items AS oi
JOIN order_item_tags AS link ON link.order_item_id = oi.id
JOIN tags AS t ON t.id = link.tag_id;
```

`BindRelation` can use `Column("order_tags")` as its single source, correlate
`order_tags.order_id` to the parent order, and bind `id` and `label` from its
row alias. Declare `id` as a unique order when a consumer observes the list
order. If the desired HFF shape includes the junction as an intermediate
collection, model each edge with a relation and place a nested relation on the
edge item. The API does not accept a join tree as `WithRelationSource`.

## Runnable example and checks

The [SQL relations example](https://github.com/ornew/hff/tree/main/examples/sql-relations)
builds the schema, relation, and filter without a database connection:

```sh
go run ./examples/sql-relations
```

It prints the generated `WHERE` fragment and the bound argument `100`. Run
the database parity suite with a dedicated PostgreSQL container:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -race -tags=integration ./backends/sql
```

See the [PostgreSQL overview](README.md) for the supported profile and
execution boundary, and [translation optimizations](optimizations.md) for
SQL shape choices and separate Build/database measurement commands.
