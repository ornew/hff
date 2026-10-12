# PostgreSQL Relation Bindings

`NewRelation` maps an HFF list to a caller-selected SQL row source, a trusted
correlation predicate, and a declared type and binding for each item. It does
not infer foreign keys or inspect a database. `Build` remains a pure
translation; the application owns joins outside the relation, database access,
transactions, and timeouts.

## Describe a correlated row source

This example maps a parent record's `items` list to rows in `hff_related`:

```go
itemType := schema.Struct(schema.Int("price"))
item := hffsql.BindStruct(
	hff.Path{},
	hffsql.TrustedSQL("true"),
	hffsql.TrustedSQL("false"),
	hffsql.Bind(hff.Path{"price"}, hffsql.Column("related", "price")),
)
relation, err := hffsql.NewRelation(
	hffsql.WithRelationSource(hffsql.Column("hff_related"), "related"),
	hffsql.WithCorrelation(hffsql.TrustedSQL(
		"related.parent_id=records.id",
		hffsql.Column("related"), hffsql.Column("records"),
	)),
	hffsql.WithRelationItem(itemType, item),
)
if err != nil {
	return err
}
items := hffsql.BindRelation(hff.Path{"items"}, relation)

query, err := hffsql.Build(ctx, program,
	hffsql.WithDialect(hffsql.PostgreSQL),
	hffsql.WithBindings(items),
)
```

The empty item path makes child bindings relative to each related row. The
declared relation item type must exactly match the HFF list element type. An
item can use a scalar `Bind`, `BindJSON`, `BindComposite`, `BindStruct`, or a
nested `BindRelation` where supported.

`WithCorrelation` takes a trusted SQL predicate. Its referenced identifiers
must be passed as `ColumnRef` values to `TrustedSQL`; the SQL text itself is
service configuration and is not parsed or escaped. The predicate must
correlate the related rows with the surrounding record. Relation aliases and
column names are quoted as identifiers by the builder.

By default a relation is present and non-null, even when it has no rows. Use
`WithRelationState(present, null)` if the physical representation must also
express a missing or null list. The expressions follow the same trusted-SQL
rules as `WithCorrelation`.

## Ordering is required only when observed

`WithUniqueOrder` declares an ordering tuple that the caller guarantees is
unique for each parent relation. Use `Ascending`, `Descending`, or
`NullsFirst` to describe each typed key. For example:

```go
hffsql.WithUniqueOrder(
	hffsql.Ascending(hffsql.Column("related", "position"), schema.IntType),
	hffsql.Ascending(hffsql.Column("related", "id"), schema.IntType),
)
```

The full tuple must be unique. An order is needed only when the HFF operation
can observe item order, such as indexing or returning an ordered list. Plain
`any()` and `all()` predicates do not require an order. If a list is transformed
or passed through a guard, the builder checks whether order remains observable
before accepting an unordered source. Unsupported or insufficient mappings
fail during `Build`.

For a plain relation path, `any()` and `all()` can lower to correlated
`EXISTS`/`NOT EXISTS` predicates. Guards, computed lists, or alias collisions
may require the general list-materialization path. Neither lowering promises
PostgreSQL's physical row or callback evaluation order, local HFF runtime
budgets, or an index plan. A visible correlation may let PostgreSQL consider
an index when the schema, indexes, statistics, and plan make it suitable.

## Validate translations

The [SQL relations example](https://github.com/ornew/hff/tree/main/examples/sql-relations)
builds a flattened struct and a correlated relation predicate without opening a
database connection:

```sh
go run ./examples/sql-relations
```

PostgreSQL parity tests cover the physical mappings and
order requirements against local HFF evaluation. Reproduce the tagged suite
with a dedicated PostgreSQL container:

```sh
HFF_POSTGRES_CONTAINER=<container-name-or-id> go test -race -tags=integration ./backends/sql
```

For the broader supported subset and execution contract, see the
[PostgreSQL overview](README.md). Translation-shape choices are listed in
[PostgreSQL translation optimizations](optimizations.md).
