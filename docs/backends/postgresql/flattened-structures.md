# Flattened PostgreSQL Struct Bindings

Use `BindStruct` when an HFF struct is stored across separate SQL columns and
the database representation does not itself encode whether the parent struct
is absent, null, or present. The caller supplies explicit, trusted SQL Boolean
expressions for parent presence and null state.

## Bind parent state and fields

```go
profile := hffsql.BindStruct(
	hff.Path{"profile"},
	hffsql.TrustedSQL("records.profile_present", hffsql.Column("records")),
	hffsql.TrustedSQL("records.profile_null", hffsql.Column("records")),
	hffsql.Bind(hff.Path{"name"}, hffsql.Column("records", "profile_name")),
	hffsql.Bind(hff.Path{"age"}, hffsql.Column("records", "profile_age")),
)

query, err := hffsql.Build(ctx, program,
	hffsql.WithDialect(hffsql.PostgreSQL),
	hffsql.WithBindings(profile),
)
```

The first path identifies the HFF struct. Child binding paths contain one
field name relative to that struct. A nested `BindStruct` supplies independent
presence and null expressions for a child struct. A nested `Bind`, `BindJSON`,
`BindComposite`, or `BindRelation` can bind a supported field representation.
Required schema fields need bindings; an unbound optional field is absent from
the HFF object.

`TrustedSQL` accepts service-authored SQL, not HFF queries or record values.
Declare referenced columns in its variadic `ColumnRef` arguments so generated
aliases cannot collide. These expressions are trusted configuration and are
not an SQL escaping boundary. Query literals continue to use bound parameters.

## Preserve HFF states

The `present` expression is true only when the struct key exists. The `null`
expression is true only when that present value is SQL null. An absent optional
parent becomes a missing HFF field; a present SQL-null parent becomes HFF null.
A present object whose child columns are all null stays an object with explicit
null children. Do not derive parent state by checking whether all child columns
are null. A scalar SQL `NULL` is an explicit null value, never a missing field.

Nested flattened parents need their own state expressions. Guarded field access
uses those state channels to retain missing, null, and no-value behavior.
`Build` does not connect to PostgreSQL or inspect rows; the service owns the
schema and must store values consistent with the HFF declaration.

For `BindStruct` combined with a PostgreSQL relation, see the
[relation binding guide](relations.md). Its runnable example also shows a
flattened parent struct and struct-bound relation item:

```sh
go run ./examples/sql-relations
```

## Limits

Flattened struct fields can be projected individually, but parent presence is
always explicit. A child path guarded by a nullable or optional parent cannot
be represented by an ordinary scalar `Bind` alone. If the database already
stores a native composite, prefer [`BindComposite`](composites.md), which
preserves SQL-null row identity separately from null attributes.
