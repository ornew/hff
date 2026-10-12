# Bind PostgreSQL Data to HFF Records

This tutorial connects a relational table to the logical record shape expected
by an HFF filter. The SQL backend builds a predicate; your application still
chooses the parent query and executes it.

The Go fragments below share these imports and form the body of a function that
receives the application's already configured database handle:

```go
import (
	"context"
	"database/sql"

	"github.com/ornew/hff"
	hffsql "github.com/ornew/hff/backends/sql"
	"github.com/ornew/hff/capabilities"
	"github.com/ornew/hff/engine"
	"github.com/ornew/hff/schema"
)

func filterOrders(db *sql.DB) error {
	ctx := context.Background()
```

## Start with the physical tables

An order has related item rows. The foreign key is part of the application's
database schema; HFF does not discover it:

```sql
CREATE TABLE orders (
    id bigint PRIMARY KEY,
    status text NOT NULL,
    tenant_id bigint NOT NULL
);
CREATE TABLE order_items (
    id bigint PRIMARY KEY,
    order_id bigint NOT NULL REFERENCES orders(id),
    price bigint NOT NULL,
    active boolean NOT NULL
);

INSERT INTO orders VALUES
    (42, 'open', 7),
    (43, 'open', 7),
    (44, 'open', 8);
INSERT INTO order_items VALUES
    (7, 42, 125, true),
    (8, 42, 35, false),
    (9, 43, 125, false),
    (10, 43, 35, true);
```

## Declare the logical HFF record

The filter sees one record with an `items` list, even though PostgreSQL stores
the items in another table. The HFF equivalent records are:

```text
order 42: matches
{"items":[{"price":125,"active":true},{"price":35,"active":false}]}
order 43: no single item satisfies both callback conditions
{"items":[{"price":125,"active":false},{"price":35,"active":true}]}
order 44: no matching child rows, so items is an empty list
{"items":[]}
```

Declare that shape in Go and compile a filter against it:

```go
item := schema.Struct(schema.Int("price"), schema.Bool("active"))
e, err := engine.New(
    engine.WithSchema(schema.Struct(schema.List("items", item))),
    engine.WithCapabilities(capabilities.Standard()),
)
if err != nil { return err }

program, err := e.Compile(ctx,
    `items:any(item=>item.price>100 item.active)`,
)
if err != nil { return err }
```

The first record matches: one item costs more than 100 and is active. The
second does not, because those conditions hold on different items. The third
does not because its list is empty. The caller's query below therefore returns
only order 42.

## Bind the list to correlated rows

`BindRelation` attaches the physical child rows to the logical `items` path.
The relation item's empty path means the row currently named `item` is the
HFF list item; its child binding paths are relative to that row:

```go
itemBinding := hffsql.BindStruct(
    hff.Path{},
    hffsql.Boolean(true),
    hffsql.Boolean(false),
    hffsql.Bind(hff.Path{"price"}, hffsql.Column("item", "price")),
    hffsql.Bind(hff.Path{"active"}, hffsql.Column("item", "active")),
)
parentOrder := hffsql.Equal(
    hffsql.ColumnValue(hffsql.Column("item", "order_id"), schema.IntType),
    hffsql.ColumnValue(hffsql.Column("orders", "id"), schema.IntType),
)
tenantMatch := hffsql.Equal(
    hffsql.ColumnValue(hffsql.Column("orders", "tenant_id"), schema.IntType),
    hffsql.Literal(hff.Int(7)),
)
relation, err := hffsql.NewRelation(
    hffsql.WithRelationSource(hffsql.Column("order_items"), "item"),
    hffsql.WithCorrelation(hffsql.And(parentOrder, tenantMatch)),
    hffsql.WithRelationItem(item, itemBinding),
)
if err != nil { return err }

query, err := hffsql.Build(ctx, program,
    hffsql.WithDialect(hffsql.PostgreSQL),
    hffsql.WithBindings(hffsql.BindRelation(
        hff.Path{"items"}, relation,
    )),
)
if err != nil { return err }
```

There are two different paths here. `hff.Path{"items"}` is the root HFF
field. `hff.Path{}` inside `BindStruct` is the item root, so
`hff.Path{"price"}` is one item field. A path such as
`hff.Path{"items", "price"}` cannot address the current item; it would try
to bind a child beneath the list. SQL identifiers are separate:
`Column("item", "price")` refers to the row alias and database column, not an
HFF field path.

## Execute the predicate in your query

`Build` returns only the predicate and its ordered arguments. The service
chooses the parent table and uses the same alias referenced by the relation
correlation. Assume these snippets are inside a function with `ctx` and an
already-open `*sql.DB`; the application chooses and configures its driver.
The tenant filter is part of the DDL above, and `parentOrder` is the typed
foreign-key correlation built in the previous block:

```go
rows, err := db.QueryContext(ctx,
    `SELECT "orders"."id" FROM "orders" WHERE `+query.Where(),
    query.Args()...,
)
if err != nil { return err }
defer rows.Close()
var ids []int64
for rows.Next() {
    var id int64
    if err := rows.Scan(&id); err != nil { return err }
    ids = append(ids, id)
}
if err := rows.Err(); err != nil { return err }
// ids contains only 42.
return nil
}
```

Pass `Args()` to the database driver as parameters; do not interpolate them
into SQL. The SQL backend does not connect to PostgreSQL, execute this
statement, or validate the table definitions or row data. The application
owns the schema contract, connection, transaction, timeout, and result scan.

The relation uses typed `Equal`, `And`, `Literal`, and `ColumnValue`
constructors for its correlation; column identifiers are quoted and their
declared HFF types must match. The tenant condition contributes a bound
argument. For predicates a typed builder cannot express, `TrustedSQL` is a raw
service-authored escape hatch. HFF does not parse or validate its syntax,
meaning, purity, or portability. Its `ColumnRef` arguments only support alias
bookkeeping. Any raw expression used for HFF state or correlation must be
deterministic and side-effect-free; HFF cannot prove that property. Keep such
expressions in reviewed service code, never concatenate query values or user
input into them, and keep HFF query literals bound through `Args()`.

## Choose the physical representation

Use the binding that matches how the value is stored:

- `Bind(path, Column(...))` maps one scalar field to one SQL column. SQL
  `NULL` is an explicit HFF null.
- `BindJSON(path, column, JSONB)` maps a schema-known object, map, or list
  stored in one JSON/JSONB document. Nested fields live inside that document.
- `BindComposite(path, column)` maps one struct to a native PostgreSQL
  composite. SQL-null composite and null attributes remain distinct; composite
  attributes cannot represent a missing field.
- `BindStruct(path, present, null, fields...)` maps one object spread over
  columns. The caller supplies separate predicates for parent presence and
  whether the reconstructed HFF value is logically null. A flattened parent
  has no single SQL value whose nullness can be inferred; do not derive its
  state from all-null child columns.
- `BindRelation(path, relation)` maps a list to correlated child rows. No
  rows mean an empty list by default, not a missing or null list.

One-to-many relations map naturally to lists. A nested one-to-many relation is
another explicit `BindRelation` on an item field. The current relation source
is one table or view plus a correlation predicate; HFF does not generate a
many-to-many join or turn a relation into a one-to-one struct. Expose a
many-to-many row shape through a view or model the junction and nested
collection as separate relations. For full SQL examples and the exact null,
missing, and ordering contracts, see the
[PostgreSQL backend guide](../backends/postgresql/README.md) and its
[relation bindings](../backends/postgresql/relations.md),
[JSON/JSONB](../backends/postgresql/json.md),
[composite](../backends/postgresql/composites.md), and
[flattened struct](../backends/postgresql/flattened-structures.md) guides.

The runnable builder example uses the same relation setup without needing a
database:

```sh
go run ./examples/sql-relations
```

## Filter native `bytea` values

The same scalar binding pattern maps a logical HFF `Bytes` field to a
PostgreSQL `bytea` column. `bytes:len()` counts bytes, `bytes:base64()` encodes
the value, and `string:frombase64()` strictly decodes standard padded base64.
The decoded result can be compared directly with a bound `bytea` field.

```sql
CREATE TABLE binary_assets (
    id bigint PRIMARY KEY,
    payload bytea NOT NULL,
    encoded text NOT NULL
);

INSERT INTO binary_assets VALUES
    (1, decode('01020304', 'hex'), 'AQIDBA=='),
    (2, decode('010203', 'hex'), 'AQIDBA=='),
    (3, decode('01020304', 'hex'), 'AQIDBA');
```

Compile a filter over the stored bytes and their textual encoding:

```go
func filterBinaryAssets(db *sql.DB) ([]int64, error) {
    ctx := context.Background()
    e, err := engine.New(
        engine.WithSchema(schema.Struct(
            schema.Bytes("payload"),
            schema.String("encoded"),
        )),
        engine.WithCapabilities(capabilities.Standard()),
    )
    if err != nil {
        return nil, err
    }

    program, err := e.Compile(ctx,
        `payload:len=4 payload:base64=encoded encoded:frombase64=payload`,
    )
    if err != nil {
        return nil, err
    }

    query, err := hffsql.Build(ctx, program,
        hffsql.WithDialect(hffsql.PostgreSQL),
        hffsql.WithBindings(
            hffsql.Bind(hff.Path{"payload"}, hffsql.Column("binary_assets", "payload")),
            hffsql.Bind(hff.Path{"encoded"}, hffsql.Column("binary_assets", "encoded")),
        ),
    )
    if err != nil {
        return nil, err
    }

    rows, err := db.QueryContext(ctx,
        `SELECT binary_assets.id FROM binary_assets WHERE `+query.Where(),
        query.Args()...,
    )
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
    return ids, nil // Contains only row 1 for the sample data above.
}
```

This function uses the imports and HFF APIs shown at the start of the tutorial;
the caller supplies an initialized database handle. Only row 1 matches. Row 2
has the wrong byte length, and row 3 has unpadded, invalid base64. Invalid
decoding yields null, so it does not match the byte comparison.

To build the same predicate without a database connection, run
`go run ./examples/sql-bytes`. It prints whether predicate construction
succeeded and how many bound parameters it produced.

PostgreSQL `bytea` is the supported stored representation for HFF Bytes.
JSON and JSONB strings can be decoded explicitly with `frombase64()`, but JSON
collections cannot store HFF Bytes values. The backend builds a predicate and
does not execute it or inspect table types; the application passes `Where()`
and `Args()` to its database driver.
